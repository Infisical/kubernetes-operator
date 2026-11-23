// Package infisicalsession resolves the InfisicalAuth and InfisicalConnection that a
// v1beta1 resource references, authenticates through the shared token cache, and
// builds API clients for the resulting session. It is shared by every v1beta1
// controller that talks to Infisical.
package infisicalsession

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-resty/resty/v2"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/cache"
	"github.com/Infisical/infisical/k8-operator/internal/conditions"
	"github.com/Infisical/infisical/k8-operator/internal/constants"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/util"
)

// ResolveAuthRef returns the given auth reference, or the default InfisicalAuth
// in the operator namespace if no reference was provided.
func ResolveAuthRef(authRef v1beta1.NamespacedName) v1beta1.NamespacedName {
	if authRef.Name != "" {
		return authRef
	}

	return v1beta1.NamespacedName{
		Name:      constants.DEFAULT_INFISICAL_AUTH_NAME,
		Namespace: constants.GetOperatorNamespace(),
	}
}

// resolveConnectionRef returns the given connection reference, or the default
// InfisicalConnection in the operator namespace if no reference was provided.
func resolveConnectionRef(connectionRef v1beta1.NamespacedName) v1beta1.NamespacedName {
	if connectionRef.Name != "" {
		return connectionRef
	}

	return v1beta1.NamespacedName{
		Name:      constants.DEFAULT_INFISICAL_CONNECTION_NAME,
		Namespace: constants.GetOperatorNamespace(),
	}
}

// getInfisicalAuth fetches the referenced InfisicalAuth, ready or not.
func getInfisicalAuth(ctx context.Context, c client.Client, isNamespaceScoped bool, authRef v1beta1.NamespacedName) (*v1beta1.InfisicalAuth, error) {
	resolved := ResolveAuthRef(authRef)

	infisicalAuth := v1beta1.InfisicalAuth{}
	err := c.Get(ctx, types.NamespacedName{Name: resolved.Name, Namespace: resolved.Namespace}, &infisicalAuth)
	if err != nil {
		if util.IsNamespaceScopedError(err, isNamespaceScoped) {
			return nil, model.NewNamespaceScopedError(err, "InfisicalAuth")
		}
		return nil, fmt.Errorf("Unable to fetch Infisical Auth CRD from cluster: %w", err)
	}

	return &infisicalAuth, nil
}

// GetInfisicalConnection fetches the referenced InfisicalConnection and returns an error unless it is ready.
func GetInfisicalConnection(ctx context.Context, c client.Client, isNamespaceScoped bool, connectionRef v1beta1.NamespacedName) (*v1beta1.InfisicalConnection, error) {
	resolved := resolveConnectionRef(connectionRef)

	conn := v1beta1.InfisicalConnection{}
	err := c.Get(ctx, types.NamespacedName{Name: resolved.Name, Namespace: resolved.Namespace}, &conn)
	if err != nil {
		if util.IsNamespaceScopedError(err, isNamespaceScoped) {
			return nil, model.NewNamespaceScopedError(err, "InfisicalConnection")
		}
		return nil, fmt.Errorf("Unable to fetch Infisical Connection CRD from cluster: %w", err)
	}

	if err := CheckConnectionNamespaces(&conn); err != nil {
		return nil, err
	}

	if err := checkReadiness("InfisicalConnection", &conn); err != nil {
		return nil, err
	}

	return &conn, nil
}

// CheckAuthNamespaces returns an error when namespace isolation forbids the
// InfisicalConnection, Secrets or ServiceAccount that the InfisicalAuth references.
func CheckAuthNamespaces(infisicalAuth *v1beta1.InfisicalAuth) error {
	namespace := infisicalAuth.Namespace

	if err := checkRefNamespace(namespace, infisicalAuth.Spec.InfisicalConnectionRef, ".spec.infisicalConnectionRef", "InfisicalConnection"); err != nil {
		return err
	}
	for _, ref := range infisicalAuth.SecretReferences() {
		if err := util.CheckNamespaceIsolation(namespace, ref.Namespace, fmt.Sprintf("the Secret %q", ref.Name)); err != nil {
			return err
		}
	}
	if k8s := infisicalAuth.Spec.Kubernetes; k8s != nil {
		return util.CheckNamespaceIsolation(namespace, k8s.ServiceAccountRef.Namespace, ".spec.kubernetes.serviceAccountRef")
	}
	return nil
}

// CheckConnectionNamespaces returns an error when namespace isolation forbids the CA
// certificate Secret that the InfisicalConnection references.
func CheckConnectionNamespaces(conn *v1beta1.InfisicalConnection) error {
	if tls := conn.Spec.TLS; tls != nil && tls.CaCertificate != nil {
		return util.CheckNamespaceIsolation(conn.Namespace, tls.CaCertificate.Namespace, ".spec.tls.caCertificate")
	}
	return nil
}

// checkRefNamespace applies namespace isolation to a reference to an InfisicalAuth or an
// InfisicalConnection. Isolation makes no exception for the default one in the operator
// namespace, which is used when the reference isn't set.
func checkRefNamespace(namespace string, ref v1beta1.NamespacedName, fieldPath, kind string) error {
	if ref.Name == "" {
		return util.CheckNamespaceIsolation(namespace, constants.GetOperatorNamespace(),
			fmt.Sprintf("%s (not set, so the default %s is used)", fieldPath, kind))
	}
	return util.CheckNamespaceIsolation(namespace, ref.Namespace, fieldPath)
}

// checkReadiness returns nil when the resource is ready. Otherwise its error names the
// resource and repeats its IsReady message, which holds the actual cause, such as
// rejected credentials or an unreachable address, so callers don't have to look it up.
func checkReadiness(resourceName string, resource readinessReporter) error {
	readyCond := meta.FindStatusCondition(resource.GetConditions(), conditions.IsReady)
	if readyCond != nil && readyCond.Status == metav1.ConditionTrue {
		return nil
	}
	if readyCond == nil || readyCond.Message == "" {
		return fmt.Errorf("%s %s/%s is not ready yet", resourceName, resource.GetNamespace(), resource.GetName())
	}
	return fmt.Errorf("%s %s/%s: %s", resourceName, resource.GetNamespace(), resource.GetName(), readyCond.Message)
}

// readinessReporter is a resource that reports whether it is ready through an IsReady condition.
type readinessReporter interface {
	metav1.Object
	GetConditions() []metav1.Condition
}

// Session is an authenticated session against an Infisical instance.
type Session struct {
	Connection  *v1beta1.InfisicalConnection
	Auth        *v1beta1.InfisicalAuth
	Credentials *model.AuthenticationResult
}

// Resolver fetches the InfisicalAuth a resource references and turns it into an
// authenticated Session. Credentials are cached per InfisicalAuth by the
// AuthStrategyResolver, so resources that share an InfisicalAuth share a single login.
type Resolver struct {
	client            client.Client
	isNamespaceScoped bool
	authResolver      *auth.AuthStrategyResolver
	projectIDCache    *cache.ResourceCache[string]
}

// NewResolver returns a Resolver. projectIDCache may be nil, in which case
// project slugs are resolved on every call.
func NewResolver(c client.Client, isNamespaceScoped bool, authResolver *auth.AuthStrategyResolver, projectIDCache *cache.ResourceCache[string]) *Resolver {
	return &Resolver{
		client:            c,
		isNamespaceScoped: isNamespaceScoped,
		authResolver:      authResolver,
		projectIDCache:    projectIDCache,
	}
}

// GetInfisicalAuth fetches the InfisicalAuth that a resource in namespace references, or
// the default one when no reference was provided. It doesn't check that the InfisicalAuth
// is ready, so callers can record its auth method even when authentication then fails;
// Authenticate does.
func (r *Resolver) GetInfisicalAuth(ctx context.Context, namespace string, authRef v1beta1.NamespacedName) (*v1beta1.InfisicalAuth, error) {
	if err := checkRefNamespace(namespace, authRef, ".spec.infisicalAuthRef", "InfisicalAuth"); err != nil {
		return nil, err
	}
	return getInfisicalAuth(ctx, r.client, r.isNamespaceScoped, authRef)
}

// Authenticate checks that the InfisicalAuth is ready, fetches the InfisicalConnection
// it references and logs in, reusing cached credentials when possible.
func (r *Resolver) Authenticate(ctx context.Context, infisicalAuth *v1beta1.InfisicalAuth) (Session, error) {
	if err := checkReadiness("InfisicalAuth", infisicalAuth); err != nil {
		return Session{}, err
	}

	// Checked here too, not only when the InfisicalAuth is reconciled, so that a Ready
	// condition from before namespace isolation was turned on doesn't skip the check.
	if err := CheckAuthNamespaces(infisicalAuth); err != nil {
		return Session{}, err
	}

	conn, err := GetInfisicalConnection(ctx, r.client, r.isNamespaceScoped, infisicalAuth.Spec.InfisicalConnectionRef)
	if err != nil {
		return Session{}, err
	}

	if err := r.authResolver.Validate(ctx, infisicalAuth); err != nil {
		return Session{}, err
	}

	credentials, err := r.authResolver.Authenticate(ctx, conn, infisicalAuth)
	if err != nil {
		return Session{}, err
	}
	if credentials == nil {
		return Session{}, fmt.Errorf("unable to get authentication credentials")
	}

	return Session{Connection: conn, Auth: infisicalAuth, Credentials: credentials}, nil
}

// NewAPIClient returns a REST client authenticated with the session's access token,
// trusting the connection's CA certificate and pointed at the connection's address.
// ctx is only used to read the CA certificate: requests made with the client are not
// bound to it.
func (r *Resolver) NewAPIClient(ctx context.Context, session Session) (*resty.Client, error) {
	if session.Connection == nil || session.Credentials == nil {
		return nil, model.ErrInvalidConnectionObject
	}

	caCertificate, err := util.ResolveTLSCaCertificate(ctx, r.client, session.Connection.Spec.TLS)
	if err != nil {
		return nil, err
	}

	restClient, err := util.CreateRestyClient(model.CreateRestyClientOptions{
		AccessToken:   session.Credentials.MachineIdentity.AccessToken,
		CaCertificate: caCertificate,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get REST client: %w", err)
	}

	return restClient.SetBaseURL(session.Connection.Address()), nil
}

// ResolveProjectID returns projectID when it is set. Otherwise it resolves projectSlug
// to a project ID, caching the result per InfisicalAuth.
func (r *Resolver) ResolveProjectID(restClient *resty.Client, authRef v1beta1.NamespacedName, projectID, projectSlug string) (string, error) {
	if projectSlug == "" {
		return projectID, nil
	}

	resolvedAuthRef := ResolveAuthRef(authRef)
	cacheKey := cache.ProjectBySlugCacheKey(resolvedAuthRef.Namespace, resolvedAuthRef.Name, projectSlug)
	if r.projectIDCache != nil {
		if cached, ok := r.projectIDCache.Get(cacheKey); ok {
			return cached, nil
		}
	}

	project, err := api.FindProjectBySlug(restClient, projectSlug)
	if err != nil {
		return "", err
	}

	if r.projectIDCache != nil {
		r.projectIDCache.Set(cacheKey, project.ID)
	}

	return project.ID, nil
}

// InvalidateOnUnauthorized drops the session's cached credentials when err reports
// that they were rejected, so the next reconcile logs in again.
func (r *Resolver) InvalidateOnUnauthorized(session Session, err error) {
	if session.Auth != nil && errors.Is(err, api.ErrUnauthorized) {
		r.authResolver.DeleteCacheEntry(session.Auth)
	}
}

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

// getInfisicalAuth fetches the referenced InfisicalAuth and returns an error unless it is ready.
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

	if !isReady(infisicalAuth.Status.Conditions) {
		return nil, fmt.Errorf("InfisicalAuth is not ready")
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

	if !isReady(conn.Status.Conditions) {
		return nil, fmt.Errorf("InfisicalConnection is not ready")
	}

	return &conn, nil
}

func isReady(statusConditions []metav1.Condition) bool {
	readyCond := meta.FindStatusCondition(statusConditions, conditions.IsReady)
	return readyCond != nil && readyCond.Status == metav1.ConditionTrue
}

// Session is an authenticated session against an Infisical instance.
type Session struct {
	Connection  *v1beta1.InfisicalConnection
	Auth        *v1beta1.InfisicalAuth
	Credentials *model.AuthenticationResult
}

// Resolver turns an InfisicalAuth reference into an authenticated Session.
// Credentials are cached per InfisicalAuth by the AuthStrategyResolver, so
// resources that share an InfisicalAuth share a single login.
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

// Authenticate resolves the referenced InfisicalAuth and its InfisicalConnection and
// logs in, reusing cached credentials when possible. The returned Session has Auth set
// as soon as the InfisicalAuth was found, even when a later step fails, so callers
// can report the auth method alongside the error.
func (r *Resolver) Authenticate(ctx context.Context, authRef v1beta1.NamespacedName) (Session, error) {
	infisicalAuth, err := getInfisicalAuth(ctx, r.client, r.isNamespaceScoped, authRef)
	if err != nil {
		return Session{}, err
	}

	session := Session{Auth: infisicalAuth}

	conn, err := GetInfisicalConnection(ctx, r.client, r.isNamespaceScoped, infisicalAuth.Spec.InfisicalConnectionRef)
	if err != nil {
		return session, err
	}
	session.Connection = conn

	if err := r.authResolver.Validate(ctx, infisicalAuth); err != nil {
		return session, err
	}

	credentials, err := r.authResolver.Authenticate(ctx, conn, infisicalAuth)
	if err != nil {
		return session, err
	}
	if credentials == nil {
		return session, fmt.Errorf("unable to get authentication credentials")
	}
	session.Credentials = credentials

	return session, nil
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

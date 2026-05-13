package auth

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/cache"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/util"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type InfisicalAuthStrategy interface {
	Validate(context.Context, *v1beta1.InfisicalAuth) error
	Authenticate(context.Context, *model.InfisicalConnection, *v1beta1.InfisicalAuth) (*model.AuthenticationResult, error)
}

type AuthStrategyResolver struct {
	entries map[v1beta1.InfisicalAuthMethod]InfisicalAuthStrategy
	client  client.Client
	cache   *cache.AuthCache
	logger  logr.Logger
}

func NewAuthStrategyResolver(client client.Client, cache *cache.AuthCache, logger logr.Logger) *AuthStrategyResolver {
	r := &AuthStrategyResolver{
		entries: make(map[v1beta1.InfisicalAuthMethod]InfisicalAuthStrategy),
		client:  client,
		cache:   cache,
		logger:  logger.WithName("AuthStrategyResolver"),
	}

	r.add(v1beta1.UniversalAuth, NewUniversalAuth(client))
	r.add(v1beta1.KubernetesAuth, NewKubernetesAuth(client))
	r.add(v1beta1.AWSIamAuth, NewAWSIamAuth())
	r.add(v1beta1.AzureAuth, NewAzureAuth())
	r.add(v1beta1.GCPIdTokenAuth, NewGCPIdTokenAuth())
	r.add(v1beta1.GCPIamAuth, NewGCPIamAuth())
	r.add(v1beta1.LDAPAuth, NewLDAPAuth(client))

	return r
}

// NewAuthStrategyResolverForTesting should not be used outside its testing file
// I didn't find a better approach for injecting mocked auth strategies.
func NewAuthStrategyResolverForTesting(cache *cache.AuthCache, providers map[v1beta1.InfisicalAuthMethod]InfisicalAuthStrategy) *AuthStrategyResolver {
	return &AuthStrategyResolver{
		entries: providers,
		cache:   cache,
		logger:  logr.New(nil),
	}
}

func (r *AuthStrategyResolver) add(method v1beta1.InfisicalAuthMethod, provider InfisicalAuthStrategy) {
	if _, found := r.entries[method]; found {
		panic(fmt.Sprintf("method %q is already defined in AuthStrategyResolver", method))
	}

	r.entries[method] = provider
}

func (r *AuthStrategyResolver) Validate(ctx context.Context, auth *v1beta1.InfisicalAuth) error {
	provider, found := r.entries[auth.Spec.Method]
	if !found {
		return fmt.Errorf("unsupported auth method: %q", auth.Spec.Method)
	}

	return provider.Validate(ctx, auth)
}

func (r *AuthStrategyResolver) Authenticate(
	ctx context.Context,
	connection *v1beta1.InfisicalConnection,
	auth *v1beta1.InfisicalAuth,
) (*model.AuthenticationResult, error) {
	provider, found := r.entries[auth.Spec.Method]
	if !found {
		return nil, fmt.Errorf("unsupported auth method: %q", auth.Spec.Method)
	}

	cacheKey := cache.ClientCacheKey{
		Name:      auth.GetObjectMeta().GetName(),
		Namespace: auth.GetObjectMeta().GetNamespace(),
		Method:    string(auth.Spec.Method),
	}

	if v, found := r.cache.Get(cacheKey); found {
		r.logger.Info(fmt.Sprintf("Reusing cached authentication: %v", cacheKey))
		return v, nil
	}

	r.logger.Info("Auth not found in cache, running authentication process")

	var caCertificate string
	if tls := connection.Spec.TLS; tls != nil && tls.CaCertificate != nil {
		certificateContent, err := util.ResolveSecretReference(ctx, r.client, *connection.Spec.TLS.CaCertificate, ".spec.tls.caCertificate")
		if err != nil {
			return nil, fmt.Errorf("Unable to authenticate: %w", err)
		}

		caCertificate = string(certificateContent)
	}

	conn := model.InfisicalConnection{
		Host:          cmp.Or(connection.Spec.Address, os.Getenv("INFISICAL_HOST_API")),
		CaCertificate: caCertificate,
	}

	authResult, err := provider.Authenticate(ctx, &conn, auth)
	if err != nil {
		return nil, err
	}

	ttl := time.Duration(authResult.MachineIdentity.ExpiresIn) * time.Second
	r.cache.Set(cacheKey, authResult, ttl)
	r.logger.Info(fmt.Sprintf("successful authentication with %q, caching credentials for %v", auth.Spec.Method, ttl))

	return authResult, nil
}

func (r *AuthStrategyResolver) DeleteCacheEntry(auth *v1beta1.InfisicalAuth) {
	cacheKey := cache.ClientCacheKey{
		Name:      auth.GetObjectMeta().GetName(),
		Namespace: auth.GetObjectMeta().GetNamespace(),
		Method:    string(auth.Spec.Method),
	}

	r.cache.Delete(cacheKey)
}

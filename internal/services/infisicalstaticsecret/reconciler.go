package infisicalstaticsecret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	tpl "text/template"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/constants"
	"github.com/Infisical/infisical/k8-operator/internal/crypto"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/template"
	"github.com/Infisical/infisical/k8-operator/internal/util"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8Errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	AutoReloadAnnotation       = "secrets.infisical.com/auto-reload"
	ManagedSecretAnnotationFmt = "secrets.infisical.com/managed-secret.%s"
)

type InfisicalStaticSecretReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	IsNamespaceScoped bool
	authResolver      *auth.AuthStrategyResolver
	logger            logr.Logger
}

func NewReconcilerForTest(c client.Client, scheme *runtime.Scheme) *InfisicalStaticSecretReconciler {
	return &InfisicalStaticSecretReconciler{
		Client: c,
		Scheme: scheme,
	}
}

func (r *InfisicalStaticSecretReconciler) getInfisicalAuth(ctx context.Context, authRef v1beta1.NamespacedName) (*v1beta1.InfisicalAuth, error) {
	auth := v1beta1.InfisicalAuth{}

	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      authRef.Name,
		Namespace: authRef.Namespace,
	}, &auth)
	if err != nil {
		if util.IsNamespaceScopedError(err, r.IsNamespaceScoped) {
			return nil, model.NewNamespaceScopedError(err, "InfisicalAuthh")
		}
		return nil, fmt.Errorf("Unable to fetch Infisical Auth CRD from cluster: %w", err)
	}

	readyCond := meta.FindStatusCondition(auth.Status.Conditions, "secrets.infisical.com/IsReady")
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		return nil, fmt.Errorf("InfisicalAuthh is not ready")
	}

	return &auth, nil
}

func (r *InfisicalStaticSecretReconciler) getInfisicalConnection(ctx context.Context, connectionRef v1beta1.NamespacedName) (*v1beta1.InfisicalConnection, error) {
	conn := v1beta1.InfisicalConnection{}

	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      connectionRef.Name,
		Namespace: connectionRef.Namespace,
	}, &conn)
	if err != nil {
		if util.IsNamespaceScopedError(err, r.IsNamespaceScoped) {
			return nil, model.NewNamespaceScopedError(err, "InfisicalConnection")
		}
		return nil, fmt.Errorf("Unable to fetch Infisical Connection CRD from cluster: %w", err)
	}

	readyCond := meta.FindStatusCondition(conn.Status.Conditions, "secrets.infisical.com/IsReady")
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		return nil, fmt.Errorf("InfisicalConnection is not ready")
	}

	return &conn, nil
}

type AuthenticateResult struct {
	Connection  *v1beta1.InfisicalConnection
	Auth        *v1beta1.InfisicalAuth
	Credentials *model.AuthenticationResult
}

func (r *InfisicalStaticSecretReconciler) Authenticate(ctx context.Context, infisicalStaticSecret *v1beta1.InfisicalStaticSecret) (AuthenticateResult, error) {
	if infisicalStaticSecret == nil {
		return AuthenticateResult{}, model.ErrInvalidStaticSecretObject
	}

	auth, err := r.getInfisicalAuth(ctx, infisicalStaticSecret.Spec.InfisicalAuthRef)
	if err != nil {
		return AuthenticateResult{}, err
	}

	if auth == nil {
		return AuthenticateResult{}, model.ErrInvalidAuthObject
	}

	conn, err := r.getInfisicalConnection(ctx, auth.Spec.InfisicalConnectionRef)
	if err != nil {
		return AuthenticateResult{}, err
	}

	if conn == nil {
		return AuthenticateResult{}, model.ErrInvalidConnectionObject
	}

	if err := r.authResolver.Validate(ctx, auth); err != nil {
		return AuthenticateResult{}, err
	}

	authResult, err := r.authResolver.Authenticate(ctx, conn, auth)
	if err != nil {
		return AuthenticateResult{}, err
	}

	if authResult == nil {
		return AuthenticateResult{}, fmt.Errorf("unable to get authentication credentials")
	}

	return AuthenticateResult{
		Connection:  conn,
		Auth:        auth,
		Credentials: authResult,
	}, nil
}

func (r *InfisicalStaticSecretReconciler) ListSecretsFromSources(ctx context.Context, infisicalStaticSecret *v1beta1.InfisicalStaticSecret, authenticationResult AuthenticateResult) ([]api.Secret, []api.Secret, error) {
	if infisicalStaticSecret == nil {
		return nil, nil, model.ErrInvalidStaticSecretObject
	}

	restClient, err := util.CreateRestyClient(model.CreateRestyClientOptions{
		AccessToken: authenticationResult.Credentials.MachineIdentity.AccessToken,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get REST client: %w", err)
	}

	restClient = restClient.SetBaseURL(authenticationResult.Connection.Spec.Address)
	requests := make([]api.ListSecretsRequest, 0, len(infisicalStaticSecret.Spec.Sources))
	for _, source := range infisicalStaticSecret.Spec.Sources {
		requests = append(requests, api.ListSecretsRequest{
			ProjectId:              source.ProjectId,
			EnvironmentSlug:        source.EnvironmentSlug,
			SecretPath:             source.SecretPath,
			Tags:                   source.Tags,
			Recursive:              source.Recursive,
			IncludeImports:         true,
			ExpandSecretReferences: true,
		})
	}

	secrets := make([]api.Secret, 0)
	importedSecrets := make([]api.Secret, 0)
	for _, request := range requests {
		response, err := api.ListSecrets(restClient, request)
		if err != nil {
			if errors.Is(err, api.ErrUnauthorized) {
				r.authResolver.DeleteCacheEntry(authenticationResult.Auth)
			}

			return nil, nil, fmt.Errorf("unable to fetch all secret sources: %w", err)
		}

		secrets = append(secrets, response.Secrets...)
		for _, imp := range response.Imports {
			importedSecrets = append(importedSecrets, imp.Secrets...)
		}
	}

	return secrets, importedSecrets, nil
}

func (r *InfisicalStaticSecretReconciler) MergeSecretSources(secrets []api.Secret, importedSecrets []api.Secret) []api.Secret {
	seen := make(map[string]struct{}, len(secrets)+len(importedSecrets))
	merged := make([]api.Secret, 0, len(secrets)+len(importedSecrets))

	for _, s := range secrets {
		if _, exists := seen[s.SecretKey]; exists {
			continue
		}
		seen[s.SecretKey] = struct{}{}
		merged = append(merged, s)
	}

	for _, s := range importedSecrets {
		if _, exists := seen[s.SecretKey]; exists {
			continue
		}
		seen[s.SecretKey] = struct{}{}
		merged = append(merged, s)
	}

	return merged
}

func (r *InfisicalStaticSecretReconciler) RenderTargetOutput(secrets []api.Secret, target v1beta1.SecretTarget) (map[string][]byte, error) {
	data := make(map[string][]byte)

	for _, s := range secrets {
		data[s.SecretKey] = []byte(s.SecretValue)
	}

	if target.Template == nil {
		return data, nil
	}

	templateCtx := make(map[string]model.SecretTemplateOptions, len(secrets))
	for _, s := range secrets {
		templateCtx[s.SecretKey] = model.SecretTemplateOptions{
			Value:      s.SecretValue,
			SecretPath: s.SecretPath,
		}
	}

	for key, tmplStr := range target.Template.Data {
		tmpl, err := tpl.New(key).Funcs(template.GetTemplateFunctions()).Parse(tmplStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse template at key %q: %w", key, err)
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, templateCtx); err != nil {
			return nil, fmt.Errorf("failed to execute template at key %q: %w", key, err)
		}

		data[key] = buf.Bytes()
	}

	return data, nil
}

// SyncKubeSecret creates or updates a Kubernetes Secrets with the secrets content
// it returns a boolean that indicates if the secret was created/updated or not
func (r *InfisicalStaticSecretReconciler) SyncKubeSecret(ctx context.Context, owner metav1.Object, data map[string][]byte, target v1beta1.SecretTarget) (bool, error) {
	newEtag := crypto.ComputeEtag([]byte(fmt.Sprintf("%v", data)))

	namespacedName := types.NamespacedName{
		Name:      target.Name,
		Namespace: target.Namespace,
	}

	existingSecret := &corev1.Secret{}
	err := r.Client.Get(ctx, namespacedName, existingSecret)

	if k8Errors.IsNotFound(err) {
		newSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      target.Name,
				Namespace: target.Namespace,
				Annotations: map[string]string{
					constants.SECRET_VERSION_ANNOTATION: newEtag,
				},
			},
			Type: target.SecretType,
			Data: data,
		}

		if target.CreationPolicy == v1beta1.CreationPolicyOwner {
			if err := ctrl.SetControllerReference(owner, newSecret, r.Scheme); err != nil {
				return false, fmt.Errorf("failed to set owner reference: %w", err)
			}
		}

		if err := r.Client.Create(ctx, newSecret); err != nil {
			return false, fmt.Errorf("failed to create secret: %w", err)
		}

		return true, nil
	}

	if err != nil {
		return false, fmt.Errorf("failed to get existing secret: %w", err)
	}

	if existingSecret.Annotations[constants.SECRET_VERSION_ANNOTATION] == newEtag {
		return false, nil
	}

	if existingSecret.Annotations == nil {
		existingSecret.Annotations = make(map[string]string)
	}
	existingSecret.Annotations[constants.SECRET_VERSION_ANNOTATION] = newEtag
	existingSecret.Data = data
	if err := r.Client.Update(ctx, existingSecret); err != nil {
		return false, fmt.Errorf("failed to update secret: %w", err)
	}

	return true, nil
}

// SyncKubeConfigMap creates or updates a Kubernetes ConfigMap with the secrets content
// it returns a boolean that indicates if the config map was created/updated or not
func (r *InfisicalStaticSecretReconciler) SyncKubeConfigMap(ctx context.Context, owner metav1.Object, data map[string][]byte, target v1beta1.SecretTarget) (bool, error) {
	newEtag := crypto.ComputeEtag([]byte(fmt.Sprintf("%v", data)))

	namespacedName := types.NamespacedName{
		Name:      target.Name,
		Namespace: target.Namespace,
	}

	existingConfigMap := &corev1.ConfigMap{}
	err := r.Client.Get(ctx, namespacedName, existingConfigMap)

	if k8Errors.IsNotFound(err) {
		stringData := make(map[string]string, len(data))
		for k, v := range data {
			stringData[k] = string(v)
		}

		newConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      target.Name,
				Namespace: target.Namespace,
				Annotations: map[string]string{
					constants.SECRET_VERSION_ANNOTATION: newEtag,
				},
			},
			Data: stringData,
		}

		if target.CreationPolicy == v1beta1.CreationPolicyOwner {
			if err := ctrl.SetControllerReference(owner, newConfigMap, r.Scheme); err != nil {
				return false, fmt.Errorf("failed to set owner reference: %w", err)
			}
		}

		if err := r.Client.Create(ctx, newConfigMap); err != nil {
			return false, fmt.Errorf("failed to create config map: %w", err)
		}

		return true, nil
	}

	if err != nil {
		return false, fmt.Errorf("failed to get existing config map: %w", err)
	}

	if existingConfigMap.Annotations[constants.SECRET_VERSION_ANNOTATION] == newEtag {
		return false, nil
	}

	stringData := make(map[string]string, len(data))
	for k, v := range data {
		stringData[k] = string(v)
	}

	if existingConfigMap.Annotations == nil {
		existingConfigMap.Annotations = make(map[string]string)
	}
	existingConfigMap.Annotations[constants.SECRET_VERSION_ANNOTATION] = newEtag
	existingConfigMap.Data = stringData
	if err := r.Client.Update(ctx, existingConfigMap); err != nil {
		return false, fmt.Errorf("failed to update config map: %w", err)
	}

	return true, nil
}

func (r *InfisicalStaticSecretReconciler) getTargetEtag(ctx context.Context, target v1beta1.SecretTarget) (string, error) {
	nn := types.NamespacedName{Name: target.Name, Namespace: target.Namespace}

	switch target.Kind {
	case v1beta1.SecretTargetKindSecret:
		s := &corev1.Secret{}
		if err := r.Client.Get(ctx, nn, s); err != nil {
			return "", fmt.Errorf("failed to get target Secret %q: %w", target.Name, err)
		}
		return s.Annotations[constants.SECRET_VERSION_ANNOTATION], nil

	case v1beta1.SecretTargetKindConfigMap:
		cm := &corev1.ConfigMap{}
		if err := r.Client.Get(ctx, nn, cm); err != nil {
			return "", fmt.Errorf("failed to get target ConfigMap %q: %w", target.Name, err)
		}
		return cm.Annotations[constants.SECRET_VERSION_ANNOTATION], nil

	default:
		return "", fmt.Errorf("unsupported target kind %q", target.Kind)
	}
}

func (r *InfisicalStaticSecretReconciler) PropagateSecretToWorkloads(ctx context.Context, target v1beta1.SecretTarget) error {
	etag, err := r.getTargetEtag(ctx, target)
	if err != nil {
		return err
	}

	annotationKey := fmt.Sprintf(ManagedSecretAnnotationFmt, target.Name)

	workloads, err := r.listWorkloadsConsumingTarget(ctx, target)
	if err != nil {
		return err
	}

	for _, workload := range workloads {
		if err := r.updateWorkloadAnnotations(ctx, workload, annotationKey, etag); err != nil {
			// TODO: should we allow one to stop executing this for all? Maybe we should not return an error as early
			// as the first error?
			return fmt.Errorf("failed to reconcile workload. Kind: %q, Name: %q", workload.kind, workload.name)
		}
	}

	return nil
}

type workloadRef struct {
	kind                string
	name                string
	annotations         map[string]string
	templateAnnotations map[string]string
	obj                 client.Object
}

func (r *InfisicalStaticSecretReconciler) listWorkloadsConsumingTarget(ctx context.Context, target v1beta1.SecretTarget) ([]workloadRef, error) {
	var workloads []workloadRef

	isConsuming := isPodSpecConsumingSecret
	if target.Kind == v1beta1.SecretTargetKindConfigMap {
		isConsuming = isPodSpecConsumingConfigMap
	}

	deployments := &appsv1.DeploymentList{}
	if err := r.Client.List(ctx, deployments, &client.ListOptions{Namespace: target.Namespace}); err != nil {
		return nil, fmt.Errorf("failed to list deployments in namespace %q: %w", target.Namespace, err)
	}

	for _, deployment := range deployments.Items {
		if deployment.Annotations[AutoReloadAnnotation] == "true" && isConsuming(deployment.Spec.Template.Spec, target.Name) {
			if deployment.Spec.Template.Annotations == nil {
				deployment.Spec.Template.Annotations = make(map[string]string)
			}
			workloads = append(workloads, workloadRef{
				kind:                "Deployment",
				name:                deployment.Name,
				annotations:         deployment.Annotations,
				templateAnnotations: deployment.Spec.Template.Annotations,
				obj:                 &deployment,
			})
		}
	}

	daemonSets := &appsv1.DaemonSetList{}
	if err := r.Client.List(ctx, daemonSets, &client.ListOptions{Namespace: target.Namespace}); err != nil {
		return nil, fmt.Errorf("failed to list daemonsets in namespace %q: %w", target.Namespace, err)
	}

	for _, daemonSet := range daemonSets.Items {
		if daemonSet.Annotations[AutoReloadAnnotation] == "true" && isConsuming(daemonSet.Spec.Template.Spec, target.Name) {
			if daemonSet.Spec.Template.Annotations == nil {
				daemonSet.Spec.Template.Annotations = make(map[string]string)
			}
			workloads = append(workloads, workloadRef{
				kind:                "DaemonSet",
				name:                daemonSet.Name,
				annotations:         daemonSet.Annotations,
				templateAnnotations: daemonSet.Spec.Template.Annotations,
				obj:                 &daemonSet,
			})
		}
	}

	statefulSets := &appsv1.StatefulSetList{}
	if err := r.Client.List(ctx, statefulSets, &client.ListOptions{Namespace: target.Namespace}); err != nil {
		return nil, fmt.Errorf("failed to list statefulsets in namespace %q: %w", target.Namespace, err)
	}

	for _, statefulSet := range statefulSets.Items {
		if statefulSet.Annotations[AutoReloadAnnotation] == "true" && isConsuming(statefulSet.Spec.Template.Spec, target.Name) {
			if statefulSet.Spec.Template.Annotations == nil {
				statefulSet.Spec.Template.Annotations = make(map[string]string)
			}
			workloads = append(workloads, workloadRef{
				kind:                "StatefulSet",
				name:                statefulSet.Name,
				annotations:         statefulSet.Annotations,
				templateAnnotations: statefulSet.Spec.Template.Annotations,
				obj:                 &statefulSet,
			})
		}
	}

	return workloads, nil
}

func (r *InfisicalStaticSecretReconciler) updateWorkloadAnnotations(ctx context.Context, w workloadRef, annotationKey, etag string) error {
	if w.annotations[annotationKey] == etag && w.templateAnnotations[annotationKey] == etag {
		return nil
	}

	w.annotations[annotationKey] = etag
	w.templateAnnotations[annotationKey] = etag

	return r.Client.Update(ctx, w.obj)
}

func isPodSpecConsumingSecret(spec corev1.PodSpec, secretName string) bool {
	for _, c := range spec.Containers {
		for _, envFrom := range c.EnvFrom {
			if envFrom.SecretRef != nil && envFrom.SecretRef.Name == secretName {
				return true
			}
		}
		for _, env := range c.Env {
			if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil && env.ValueFrom.SecretKeyRef.Name == secretName {
				return true
			}
		}
	}
	for _, v := range spec.Volumes {
		if v.Secret != nil && v.Secret.SecretName == secretName {
			return true
		}
	}
	return false
}

func isPodSpecConsumingConfigMap(spec corev1.PodSpec, configMapName string) bool {
	for _, c := range spec.Containers {
		for _, envFrom := range c.EnvFrom {
			if envFrom.ConfigMapRef != nil && envFrom.ConfigMapRef.Name == configMapName {
				return true
			}
		}
		for _, env := range c.Env {
			if env.ValueFrom != nil && env.ValueFrom.ConfigMapKeyRef != nil && env.ValueFrom.ConfigMapKeyRef.Name == configMapName {
				return true
			}
		}
	}
	for _, v := range spec.Volumes {
		if v.ConfigMap != nil && v.ConfigMap.Name == configMapName {
			return true
		}
	}
	return false
}

package infisicalstaticsecret

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/constants"
	"github.com/Infisical/infisical/k8-operator/internal/crypto"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	templatev1 "github.com/Infisical/infisical/k8-operator/internal/template/v1"
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

var systemAnnotationPrefixes = []string{"kubectl.kubernetes.io/", "kubernetes.io/", "k8s.io/", "helm.sh/"}

func isSystemAnnotation(key string) bool {
	for _, prefix := range systemAnnotationPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func parseManagedKeys(value string) map[string]bool {
	keys := make(map[string]bool)
	if value == "" {
		return keys
	}
	for _, k := range strings.Split(value, ",") {
		k = strings.TrimSpace(k)
		if k != "" {
			keys[k] = true
		}
	}
	return keys
}

func formatManagedKeys(keys map[string]bool) string {
	if len(keys) == 0 {
		return ""
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// computeTargetMetadata determines the labels and annotations to apply to a managed resource.
// When target.Metadata is set, those values are injected directly.
// Otherwise, labels and annotations are merged from the owner CRD with tracking for cleanup.
func computeTargetMetadata(owner metav1.Object, target v1beta1.SecretTarget, existingAnnotations, existingLabels map[string]string) (labels, annotations map[string]string) {
	if target.Metadata != nil {
		labels = make(map[string]string, len(target.Metadata.Labels))
		for k, v := range target.Metadata.Labels {
			labels[k] = v
		}

		annotations = make(map[string]string)
		for k, v := range existingAnnotations {
			if isSystemAnnotation(k) || k == constants.SECRET_VERSION_ANNOTATION {
				annotations[k] = v
			}
		}
		for k, v := range target.Metadata.Annotations {
			annotations[k] = v
		}
		return labels, annotations
	}

	// Merge from owner CRD with tracking
	previouslyManagedLabels := parseManagedKeys(existingAnnotations[constants.MANAGED_LABELS_ANNOTATION])
	previouslyManagedAnnotations := parseManagedKeys(existingAnnotations[constants.MANAGED_ANNOTATIONS_ANNOTATION])

	labels = make(map[string]string)
	for k, v := range existingLabels {
		if !previouslyManagedLabels[k] {
			labels[k] = v
		}
	}
	for k, v := range owner.GetLabels() {
		labels[k] = v
	}

	annotations = make(map[string]string)
	for k, v := range existingAnnotations {
		if isSystemAnnotation(k) || k == constants.SECRET_VERSION_ANNOTATION || k == constants.MANAGED_LABELS_ANNOTATION || k == constants.MANAGED_ANNOTATIONS_ANNOTATION {
			annotations[k] = v
		} else if !previouslyManagedAnnotations[k] {
			annotations[k] = v
		}
	}
	for k, v := range owner.GetAnnotations() {
		if !isSystemAnnotation(k) {
			annotations[k] = v
		}
	}

	currentLabelKeys := make(map[string]bool)
	for k := range owner.GetLabels() {
		currentLabelKeys[k] = true
	}
	currentAnnotationKeys := make(map[string]bool)
	for k := range owner.GetAnnotations() {
		if !isSystemAnnotation(k) {
			currentAnnotationKeys[k] = true
		}
	}
	annotations[constants.MANAGED_LABELS_ANNOTATION] = formatManagedKeys(currentLabelKeys)
	annotations[constants.MANAGED_ANNOTATIONS_ANNOTATION] = formatManagedKeys(currentAnnotationKeys)

	return labels, annotations
}

type InfisicalStaticSecretReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	IsNamespaceScoped bool
	authResolver      *auth.AuthStrategyResolver
	logger            logr.Logger
}

func (r *InfisicalStaticSecretReconciler) Validate(infisicalStaticSecret *v1beta1.InfisicalStaticSecret) error {
	if infisicalStaticSecret == nil {
		return model.ErrInvalidStaticSecretObject
	}

	spec := &infisicalStaticSecret.Spec

	if spec.SyncOptions == nil {
		return fmt.Errorf("syncOptions is required")
	}

	if _, err := util.ConvertResyncIntervalToDuration(spec.SyncOptions.RefreshInterval, false); err != nil {
		return fmt.Errorf("invalid refreshInterval %q: %w", spec.SyncOptions.RefreshInterval, err)
	}

	if len(spec.Sources) == 0 {
		return fmt.Errorf("at least one source is required")
	}

	for i, source := range spec.Sources {
		if source.ProjectId == "" && source.ProjectSlug == "" {
			return fmt.Errorf("either sources[%d].projectId or sources[%d].projectSlug must be set", i, i)
		}
		if source.ProjectId != "" && source.ProjectSlug != "" {
			return fmt.Errorf("you declared both sources[%d].projectId and sources[%d].projectSlug: you should declare only one", i, i)
		}
		if source.EnvironmentSlug == "" {
			return fmt.Errorf("sources[%d].environmentSlug is required", i)
		}
		if source.SecretPath == "" {
			return fmt.Errorf("sources[%d].secretPath is required", i)
		}
	}

	if len(spec.Targets) == 0 {
		return fmt.Errorf("at least one target is required")
	}

	targetNames := make(map[string]struct{}, len(spec.Targets))
	for i, target := range spec.Targets {
		if target.Name == "" {
			return fmt.Errorf("targets[%d].name is required", i)
		}
		if target.Namespace == "" {
			return fmt.Errorf("targets[%d].namespace is required", i)
		}

		if target.Kind != v1beta1.SecretTargetKindConfigMap && target.Kind != v1beta1.SecretTargetKindSecret {
			return fmt.Errorf("targets[%d].target is invalid", i)
		}

		key := fmt.Sprintf("%s/%s", target.Namespace, target.Name)
		if _, exists := targetNames[key]; exists {
			return fmt.Errorf("duplicate target %q", key)
		}
		targetNames[key] = struct{}{}

		if target.SecretType != "" && target.Kind != v1beta1.SecretTargetKindSecret {
			return fmt.Errorf("targets[%d].secretType is only valid for Secret targets", i)
		}
	}

	return nil
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
			return nil, model.NewNamespaceScopedError(err, "InfisicalAuth")
		}
		return nil, fmt.Errorf("Unable to fetch Infisical Auth CRD from cluster: %w", err)
	}

	readyCond := meta.FindStatusCondition(auth.Status.Conditions, "secrets.infisical.com/IsReady")
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		return nil, fmt.Errorf("InfisicalAuth is not ready")
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

	setAuthMethodCondition(infisicalStaticSecret, string(auth.Spec.Method))

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

	restClient = restClient.SetBaseURL(authenticationResult.Connection.Address())
	requests := make([]api.ListSecretsRequest, 0, len(infisicalStaticSecret.Spec.Sources))
	for _, source := range infisicalStaticSecret.Spec.Sources {
		projectID := source.ProjectId
		if source.ProjectSlug != "" {
			project, err := api.FindProjectBySlug(restClient, source.ProjectSlug)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to list secrets: %w", err)
			}
			projectID = project.ID
		}

		requests = append(requests, api.ListSecretsRequest{
			ProjectId:       projectID,
			EnvironmentSlug: source.EnvironmentSlug,
			SecretPath:      source.SecretPath,
			Tags:            source.TagSlugs,
			Recursive:       source.Recursive,
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

func (r *InfisicalStaticSecretReconciler) RenderTargetOutput(mergedSecrets, rawSecrets []api.Secret, target v1beta1.SecretTarget) (map[string][]byte, error) {
	data := make(map[string][]byte)

	for _, s := range mergedSecrets {
		data[s.SecretKey] = []byte(s.SecretValue)
	}

	if target.Template == nil {
		return data, nil
	}

	templateCtx := templatev1.NewTemplateContext(rawSecrets, mergedSecrets)
	if target.Template.Data.IsRaw() {
		return templatev1.RenderBulkTemplate(target.Template.Data.Raw, templateCtx)
	}

	return templatev1.RenderPerKeyTemplates(target.Template.Data.Map, templateCtx)
}

// SyncKubeSecret creates or updates a Kubernetes Secret with the secrets content.
// Returns (changed, etag, error). The etag is the version written to the annotation.
func (r *InfisicalStaticSecretReconciler) SyncKubeSecret(ctx context.Context, owner metav1.Object, data map[string][]byte, target v1beta1.SecretTarget) (bool, string, error) {
	newEtag := crypto.ComputeEtag([]byte(fmt.Sprintf("%v", data)))

	namespacedName := types.NamespacedName{
		Name:      target.Name,
		Namespace: target.Namespace,
	}

	existingSecret := &corev1.Secret{}
	err := r.Client.Get(ctx, namespacedName, existingSecret)

	if k8Errors.IsNotFound(err) {
		labels, annotations := computeTargetMetadata(owner, target, nil, nil)
		annotations[constants.SECRET_VERSION_ANNOTATION] = newEtag

		newSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        target.Name,
				Namespace:   target.Namespace,
				Labels:      labels,
				Annotations: annotations,
			},
			Type: target.SecretType,
			Data: data,
		}

		if target.CreationPolicy == v1beta1.CreationPolicyOwner {
			if err := ctrl.SetControllerReference(owner, newSecret, r.Scheme); err != nil {
				return false, "", fmt.Errorf("failed to set owner reference: %w", err)
			}
		}

		if err := r.Client.Create(ctx, newSecret); err != nil {
			return false, "", fmt.Errorf("failed to create secret: %w", err)
		}

		return true, newEtag, nil
	}

	if err != nil {
		return false, "", fmt.Errorf("failed to get existing secret: %w", err)
	}

	dataChanged := existingSecret.Annotations[constants.SECRET_VERSION_ANNOTATION] != newEtag

	labels, annotations := computeTargetMetadata(owner, target, existingSecret.Annotations, existingSecret.Labels)
	annotations[constants.SECRET_VERSION_ANNOTATION] = newEtag

	metadataChanged := !maps.Equal(existingSecret.Labels, labels) || !maps.Equal(existingSecret.Annotations, annotations)

	if !dataChanged && !metadataChanged {
		return false, newEtag, nil
	}

	existingSecret.Labels = labels
	existingSecret.Annotations = annotations
	existingSecret.Data = data
	if err := r.Client.Update(ctx, existingSecret); err != nil {
		return false, "", fmt.Errorf("failed to update secret: %w", err)
	}

	return dataChanged, newEtag, nil
}

// SyncKubeConfigMap creates or updates a Kubernetes ConfigMap with the secrets content.
// Returns (changed, etag, error). The etag is the version written to the annotation.
func (r *InfisicalStaticSecretReconciler) SyncKubeConfigMap(ctx context.Context, owner metav1.Object, data map[string][]byte, target v1beta1.SecretTarget) (bool, string, error) {
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

		labels, annotations := computeTargetMetadata(owner, target, nil, nil)
		annotations[constants.SECRET_VERSION_ANNOTATION] = newEtag

		newConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:        target.Name,
				Namespace:   target.Namespace,
				Labels:      labels,
				Annotations: annotations,
			},
			Data: stringData,
		}

		if target.CreationPolicy == v1beta1.CreationPolicyOwner {
			if err := ctrl.SetControllerReference(owner, newConfigMap, r.Scheme); err != nil {
				return false, "", fmt.Errorf("failed to set owner reference: %w", err)
			}
		}

		if err := r.Client.Create(ctx, newConfigMap); err != nil {
			return false, "", fmt.Errorf("failed to create config map: %w", err)
		}

		return true, newEtag, nil
	}

	if err != nil {
		return false, "", fmt.Errorf("failed to get existing config map: %w", err)
	}

	dataChanged := existingConfigMap.Annotations[constants.SECRET_VERSION_ANNOTATION] != newEtag

	labels, annotations := computeTargetMetadata(owner, target, existingConfigMap.Annotations, existingConfigMap.Labels)
	annotations[constants.SECRET_VERSION_ANNOTATION] = newEtag

	metadataChanged := !maps.Equal(existingConfigMap.Labels, labels) || !maps.Equal(existingConfigMap.Annotations, annotations)

	if !dataChanged && !metadataChanged {
		return false, newEtag, nil
	}

	stringData := make(map[string]string, len(data))
	for k, v := range data {
		stringData[k] = string(v)
	}

	existingConfigMap.Labels = labels
	existingConfigMap.Annotations = annotations
	existingConfigMap.Data = stringData
	if err := r.Client.Update(ctx, existingConfigMap); err != nil {
		return false, "", fmt.Errorf("failed to update config map: %w", err)
	}

	return dataChanged, newEtag, nil
}

func (r *InfisicalStaticSecretReconciler) PropagateSecretToWorkloads(ctx context.Context, target v1beta1.SecretTarget, etag string) (int, error) {
	annotationKey := fmt.Sprintf(ManagedSecretAnnotationFmt, target.Name)

	workloads, err := r.listWorkloadsConsumingTarget(ctx, target)
	if err != nil {
		return 0, err
	}

	// Prevents one workload from stopping our operator from updating all others
	var errs []error
	for _, workload := range workloads {
		if err := r.updateWorkloadAnnotations(ctx, workload, annotationKey, etag); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile workload. Kind: %q, Name: %q", workload.kind, workload.name))
		}
	}

	return len(workloads), errors.Join(errs...)
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
	if w.annotations == nil {
		w.annotations = make(map[string]string)
	}

	if w.templateAnnotations == nil {
		w.templateAnnotations = make(map[string]string)
	}

	if w.annotations[annotationKey] == etag && w.templateAnnotations[annotationKey] == etag {
		return nil
	}

	w.annotations[annotationKey] = etag
	w.templateAnnotations[annotationKey] = etag

	return r.Client.Update(ctx, w.obj)
}

func isContainerConsumingSecret(containers []corev1.Container, secretName string) bool {
	for _, c := range containers {
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
	return false
}

func isPodSpecConsumingSecret(spec corev1.PodSpec, secretName string) bool {
	if isContainerConsumingSecret(spec.Containers, secretName) || isContainerConsumingSecret(spec.InitContainers, secretName) {
		return true
	}
	for _, v := range spec.Volumes {
		if v.Secret != nil && v.Secret.SecretName == secretName {
			return true
		}
	}
	return false
}

func isContainerConsumingConfigMap(containers []corev1.Container, configMapName string) bool {
	for _, c := range containers {
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
	return false
}

func isPodSpecConsumingConfigMap(spec corev1.PodSpec, configMapName string) bool {
	if isContainerConsumingConfigMap(spec.Containers, configMapName) || isContainerConsumingConfigMap(spec.InitContainers, configMapName) {
		return true
	}
	for _, v := range spec.Volumes {
		if v.ConfigMap != nil && v.ConfigMap.Name == configMapName {
			return true
		}
	}
	return false
}

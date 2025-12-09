package infisicalsecret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	tpl "text/template"

	"github.com/Infisical/infisical/k8-operator/api/v1alpha1"
	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/config"
	"github.com/Infisical/infisical/k8-operator/internal/constants"
	"github.com/Infisical/infisical/k8-operator/internal/crypto"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/template"
	"github.com/Infisical/infisical/k8-operator/internal/util"
	"github.com/Infisical/infisical/k8-operator/internal/util/sse"
	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	infisicalSdk "github.com/infisical/go-sdk"
	corev1 "k8s.io/api/core/v1"
	k8Errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

const FINALIZER_NAME = "secrets.finalizers.infisical.com"

var SYSTEM_PREFIXES = []string{"kubectl.kubernetes.io/", "kubernetes.io/", "k8s.io/", "helm.sh/"}

type InfisicalSecretReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	IsNamespaceScoped bool
}

func (r *InfisicalSecretReconciler) getInfisicalTokenFromKubeSecret(ctx context.Context, infisicalSecret v1alpha1.InfisicalSecret) (string, error) {
	// default to new secret ref structure
	secretName := infisicalSecret.Spec.Authentication.ServiceToken.ServiceTokenSecretReference.SecretName
	secretNamespace := infisicalSecret.Spec.Authentication.ServiceToken.ServiceTokenSecretReference.SecretNamespace
	// fall back to previous secret ref
	if secretName == "" {
		secretName = infisicalSecret.Spec.TokenSecretReference.SecretName
	}

	if secretNamespace == "" {
		secretNamespace = infisicalSecret.Spec.TokenSecretReference.SecretNamespace
	}

	tokenSecret, err := util.GetKubeSecretByNamespacedName(ctx, r.Client, types.NamespacedName{
		Namespace: secretNamespace,
		Name:      secretName,
	})

	if k8Errors.IsNotFound(err) || (secretNamespace == "" && secretName == "") {
		return "", nil
	}

	if err != nil {
		if util.IsNamespaceScopedError(err, r.IsNamespaceScoped) {
			return "", fmt.Errorf("unable to fetch Kubernetes CA certificate secret. Your Operator installation is namespace scoped, and cannot read secrets outside of the namespace it is installed in. Please ensure the CA certificate secret is in the same namespace as the operator. [err=%v]", err)
		}
		return "", fmt.Errorf("failed to read Infisical token secret from secret named [%s] in namespace [%s]: with error [%w]", infisicalSecret.Spec.TokenSecretReference.SecretName, infisicalSecret.Spec.TokenSecretReference.SecretNamespace, err)
	}

	infisicalServiceToken := tokenSecret.Data[constants.INFISICAL_TOKEN_SECRET_KEY_NAME]

	return strings.Replace(string(infisicalServiceToken), " ", "", -1), nil
}

// Fetches service account credentials from a Kubernetes secret specified in the infisicalSecret object, extracts the access key, public key, and private key from the secret, and returns them as a ServiceAccountCredentials object.
// If any keys are missing or an error occurs, returns an empty object or an error object, respectively.
func (r *InfisicalSecretReconciler) getInfisicalServiceAccountCredentialsFromKubeSecret(ctx context.Context, infisicalSecret v1alpha1.InfisicalSecret) (serviceAccountDetails model.ServiceAccountDetails, err error) {

	secretNamespace := infisicalSecret.Spec.Authentication.ServiceAccount.ServiceAccountSecretReference.SecretNamespace
	secretName := infisicalSecret.Spec.Authentication.ServiceAccount.ServiceAccountSecretReference.SecretName

	serviceAccountCredsFromKubeSecret, err := util.GetKubeSecretByNamespacedName(ctx, r.Client, types.NamespacedName{
		Namespace: secretNamespace,
		Name:      secretName,
	})

	if k8Errors.IsNotFound(err) || (secretNamespace == "" && secretName == "") {
		return model.ServiceAccountDetails{}, nil
	}

	if err != nil {
		if util.IsNamespaceScopedError(err, r.IsNamespaceScoped) {
			return model.ServiceAccountDetails{}, fmt.Errorf("unable to fetch Kubernetes service account credentials secret. Your Operator installation is namespace scoped, and cannot read secrets outside of the namespace it is installed in. Please ensure the service account credentials secret is in the same namespace as the operator. [err=%v]", err)
		}
		return model.ServiceAccountDetails{}, fmt.Errorf("something went wrong when fetching your service account credentials [err=%s]", err)
	}

	accessKeyFromSecret := serviceAccountCredsFromKubeSecret.Data[constants.SERVICE_ACCOUNT_ACCESS_KEY]
	publicKeyFromSecret := serviceAccountCredsFromKubeSecret.Data[constants.SERVICE_ACCOUNT_PUBLIC_KEY]
	privateKeyFromSecret := serviceAccountCredsFromKubeSecret.Data[constants.SERVICE_ACCOUNT_PRIVATE_KEY]

	if accessKeyFromSecret == nil || publicKeyFromSecret == nil || privateKeyFromSecret == nil {
		return model.ServiceAccountDetails{}, nil
	}

	return model.ServiceAccountDetails{AccessKey: string(accessKeyFromSecret), PrivateKey: string(privateKeyFromSecret), PublicKey: string(publicKeyFromSecret)}, nil
}

func convertBinaryToStringMap(binaryMap map[string][]byte) map[string]string {
	stringMap := make(map[string]string)
	for k, v := range binaryMap {
		stringMap[k] = string(v)
	}
	return stringMap
}

func (r *InfisicalSecretReconciler) createInfisicalManagedKubeResource(ctx context.Context, logger logr.Logger, infisicalSecret v1alpha1.InfisicalSecret, managedSecretReferenceInterface interface{}, secretsFromAPI []model.SingleEnvironmentVariable, ETag string, resourceType constants.ManagedKubeResourceType) error {
	plainProcessedSecrets := make(map[string][]byte)

	var managedTemplateData *v1alpha1.SecretTemplate

	if resourceType == constants.MANAGED_KUBE_RESOURCE_TYPE_SECRET {
		managedTemplateData = managedSecretReferenceInterface.(v1alpha1.ManagedKubeSecretConfig).Template
	} else if resourceType == constants.MANAGED_KUBE_RESOURCE_TYPE_CONFIG_MAP {
		managedTemplateData = managedSecretReferenceInterface.(v1alpha1.ManagedKubeConfigMapConfig).Template
	}

	if managedTemplateData == nil || managedTemplateData.IncludeAllSecrets {
		for _, secret := range secretsFromAPI {
			plainProcessedSecrets[secret.Key] = []byte(secret.Value) // plain process
		}
	}

	if managedTemplateData != nil {
		secretKeyValue := make(map[string]model.SecretTemplateOptions)
		for _, secret := range secretsFromAPI {
			secretKeyValue[secret.Key] = model.SecretTemplateOptions{
				Value:      secret.Value,
				SecretPath: secret.SecretPath,
			}
		}

		for templateKey, userTemplate := range managedTemplateData.Data {
			tmpl, err := tpl.New("secret-templates").Funcs(template.GetTemplateFunctions()).Parse(userTemplate)
			if err != nil {
				return fmt.Errorf("unable to compile template: %s [err=%v]", templateKey, err)
			}

			buf := bytes.NewBuffer(nil)
			err = tmpl.Execute(buf, secretKeyValue)
			if err != nil {
				return fmt.Errorf("unable to execute template: %s [err=%v]", templateKey, err)
			}
			plainProcessedSecrets[templateKey] = buf.Bytes()
		}
	}

	// copy labels and annotations from InfisicalSecret CRD
	labels := map[string]string{}
	for k, v := range infisicalSecret.Labels {
		labels[k] = v
	}

	annotations := map[string]string{}
	for k, v := range infisicalSecret.Annotations {
		isSystem := false
		for _, prefix := range SYSTEM_PREFIXES {
			if strings.HasPrefix(k, prefix) {
				isSystem = true
				break
			}
		}
		if !isSystem {
			annotations[k] = v
		}
	}

	// Track which labels and annotations we manage
	managedLabelKeys := make(map[string]bool)
	for k := range infisicalSecret.Labels {
		managedLabelKeys[k] = true
	}
	managedAnnotationKeys := make(map[string]bool)
	for k := range infisicalSecret.Annotations {
		isSystem := false
		for _, prefix := range SYSTEM_PREFIXES {
			if strings.HasPrefix(k, prefix) {
				isSystem = true
				break
			}
		}
		if !isSystem {
			managedAnnotationKeys[k] = true
		}
	}
	annotations[constants.MANAGED_LABELS_ANNOTATION] = formatManagedKeys(managedLabelKeys)
	annotations[constants.MANAGED_ANNOTATIONS_ANNOTATION] = formatManagedKeys(managedAnnotationKeys)

	if resourceType == constants.MANAGED_KUBE_RESOURCE_TYPE_SECRET {

		managedSecretReference := managedSecretReferenceInterface.(v1alpha1.ManagedKubeSecretConfig)

		annotations[constants.SECRET_VERSION_ANNOTATION] = ETag
		// create a new secret as specified by the managed secret spec of CRD
		newKubeSecretInstance := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        managedSecretReference.SecretName,
				Namespace:   managedSecretReference.SecretNamespace,
				Annotations: annotations,
				Labels:      labels,
			},
			Type: corev1.SecretType(managedSecretReference.SecretType),
			Data: plainProcessedSecrets,
		}

		if managedSecretReference.CreationPolicy == "Owner" {
			// Set InfisicalSecret instance as the owner and controller of the managed secret
			err := ctrl.SetControllerReference(&infisicalSecret, newKubeSecretInstance, r.Scheme)
			if err != nil {
				return err
			}
		}

		err := r.Client.Create(ctx, newKubeSecretInstance)
		if err != nil {
			return fmt.Errorf("unable to create the managed Kubernetes secret : %w", err)
		}
		logger.Info(fmt.Sprintf("Successfully created a managed Kubernetes secret with your Infisical secrets. Type: %s", managedSecretReference.SecretType))
		return nil
	} else if resourceType == constants.MANAGED_KUBE_RESOURCE_TYPE_CONFIG_MAP {

		managedSecretReference := managedSecretReferenceInterface.(v1alpha1.ManagedKubeConfigMapConfig)

		// create a new config map as specified by the managed secret spec of CRD
		newKubeConfigMapInstance := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:        managedSecretReference.ConfigMapName,
				Namespace:   managedSecretReference.ConfigMapNamespace,
				Annotations: annotations,
				Labels:      labels,
			},
			Data: convertBinaryToStringMap(plainProcessedSecrets),
		}

		if managedSecretReference.CreationPolicy == "Owner" {
			// Set InfisicalSecret instance as the owner and controller of the managed config map
			err := ctrl.SetControllerReference(&infisicalSecret, newKubeConfigMapInstance, r.Scheme)
			if err != nil {
				return err
			}
		}

		err := r.Client.Create(ctx, newKubeConfigMapInstance)
		if err != nil {
			return fmt.Errorf("unable to create the managed Kubernetes config map : %w", err)
		}
		logger.Info(fmt.Sprintf("Successfully created a managed Kubernetes config map with your Infisical secrets. Type: %s", managedSecretReference.ConfigMapName))
		return nil

	}
	return fmt.Errorf("invalid resource type")

}

func parseManagedKeys(value string) map[string]bool {
	managedKeys := make(map[string]bool)
	if value == "" {
		return managedKeys
	}
	keys := strings.Split(value, ",")
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" {
			managedKeys[key] = true
		}
	}
	return managedKeys
}

func formatManagedKeys(keys map[string]bool) string {
	if len(keys) == 0 {
		return ""
	}
	keyList := make([]string, 0, len(keys))
	for k := range keys {
		keyList = append(keyList, k)
	}
	sort.Strings(keyList)
	return strings.Join(keyList, ",")
}

// syncLabelsAndAnnotations syncs labels and annotations from the InfisicalSecret CRD to the managed resource.
// It ensures that labels/annotations removed from the CRD are also removed from the managed resource.
// Labels/annotations that exist on the managed resource but are NOT in the CRD are preserved.
func (r *InfisicalSecretReconciler) syncLabelsAndAnnotations(infisicalSecret v1alpha1.InfisicalSecret, existingAnnotations map[string]string, existingLabels map[string]string) (map[string]string, map[string]string) {
	// get previously managed keys from tracking annotations
	previouslyManagedLabels := parseManagedKeys(existingAnnotations[constants.MANAGED_LABELS_ANNOTATION])
	previouslyManagedAnnotations := parseManagedKeys(existingAnnotations[constants.MANAGED_ANNOTATIONS_ANNOTATION])

	// build sets of current CRD label/annotation keys
	currentCrdLabelKeys := make(map[string]bool)
	for k := range infisicalSecret.Labels {
		currentCrdLabelKeys[k] = true
	}

	currentCrdAnnotationKeys := make(map[string]bool)
	for k := range infisicalSecret.Annotations {
		isSystem := false
		for _, prefix := range SYSTEM_PREFIXES {
			if strings.HasPrefix(k, prefix) {
				isSystem = true
				break
			}
		}
		if !isSystem {
			currentCrdAnnotationKeys[k] = true
		}
	}

	// build new labels, keep labels not managed by us, remove previously managed but no longer in CRD
	newLabels := make(map[string]string)
	for k, v := range existingLabels {
		// keep labels that were never managed by us
		if !previouslyManagedLabels[k] {
			newLabels[k] = v
		}
	}
	// add/update labels from crd
	for k, v := range infisicalSecret.Labels {
		newLabels[k] = v
	}

	// same as above, but for annotations
	newAnnotations := make(map[string]string)
	for k, v := range existingAnnotations {
		isSystem := false
		for _, prefix := range SYSTEM_PREFIXES {
			if strings.HasPrefix(k, prefix) {
				isSystem = true
				break
			}
		}
		if isSystem || k == constants.SECRET_VERSION_ANNOTATION || k == constants.MANAGED_LABELS_ANNOTATION || k == constants.MANAGED_ANNOTATIONS_ANNOTATION {
			newAnnotations[k] = v
		} else if !previouslyManagedAnnotations[k] {
			// keep annotations that were never managed by us
			newAnnotations[k] = v
		}
	}

	for k, v := range infisicalSecret.Annotations {
		isSystem := false
		for _, prefix := range SYSTEM_PREFIXES {
			if strings.HasPrefix(k, prefix) {
				isSystem = true
				break
			}
		}
		if !isSystem {
			newAnnotations[k] = v
		}
	}

	// update tracking annotations with current managed keys
	newAnnotations[constants.MANAGED_LABELS_ANNOTATION] = formatManagedKeys(currentCrdLabelKeys)
	newAnnotations[constants.MANAGED_ANNOTATIONS_ANNOTATION] = formatManagedKeys(currentCrdAnnotationKeys)

	return newAnnotations, newLabels
}

func (r *InfisicalSecretReconciler) updateInfisicalManagedKubeSecret(ctx context.Context, logger logr.Logger, infisicalSecret v1alpha1.InfisicalSecret, managedSecretReference v1alpha1.ManagedKubeSecretConfig, managedKubeSecret corev1.Secret, secretsFromAPI []model.SingleEnvironmentVariable, ETag string) error {
	managedTemplateData := managedSecretReference.Template

	plainProcessedSecrets := make(map[string][]byte)
	if managedTemplateData == nil || managedTemplateData.IncludeAllSecrets {
		for _, secret := range secretsFromAPI {
			plainProcessedSecrets[secret.Key] = []byte(secret.Value)
		}
	}

	if managedTemplateData != nil {
		secretKeyValue := make(map[string]model.SecretTemplateOptions)
		for _, secret := range secretsFromAPI {
			secretKeyValue[secret.Key] = model.SecretTemplateOptions{
				Value:      secret.Value,
				SecretPath: secret.SecretPath,
			}
		}

		for templateKey, userTemplate := range managedTemplateData.Data {
			tmpl, err := tpl.New("secret-templates").Funcs(template.GetTemplateFunctions()).Parse(userTemplate)
			if err != nil {
				return fmt.Errorf("unable to compile template: %s [err=%v]", templateKey, err)
			}

			buf := bytes.NewBuffer(nil)
			err = tmpl.Execute(buf, secretKeyValue)
			if err != nil {
				return fmt.Errorf("unable to execute template: %s [err=%v]", templateKey, err)
			}
			plainProcessedSecrets[templateKey] = buf.Bytes()
		}
	}

	// Sync labels and annotations from CRD (removes ones no longer in CRD)
	newAnnotations, newLabels := r.syncLabelsAndAnnotations(infisicalSecret, managedKubeSecret.ObjectMeta.Annotations, managedKubeSecret.ObjectMeta.Labels)

	managedKubeSecret.ObjectMeta.Labels = newLabels
	managedKubeSecret.ObjectMeta.Annotations = newAnnotations
	managedKubeSecret.Data = plainProcessedSecrets
	managedKubeSecret.ObjectMeta.Annotations[constants.SECRET_VERSION_ANNOTATION] = ETag

	err := r.Client.Update(ctx, &managedKubeSecret)
	if err != nil {
		return fmt.Errorf("unable to update Kubernetes secret because [%w]", err)
	}

	logger.Info("successfully updated managed Kubernetes secret")
	return nil
}

func (r *InfisicalSecretReconciler) updateInfisicalManagedConfigMap(ctx context.Context, logger logr.Logger, infisicalSecret v1alpha1.InfisicalSecret, managedConfigMapReference v1alpha1.ManagedKubeConfigMapConfig, managedConfigMap corev1.ConfigMap, secretsFromAPI []model.SingleEnvironmentVariable, ETag string) error {
	managedTemplateData := managedConfigMapReference.Template

	plainProcessedSecrets := make(map[string][]byte)
	if managedTemplateData == nil || managedTemplateData.IncludeAllSecrets {
		for _, secret := range secretsFromAPI {
			plainProcessedSecrets[secret.Key] = []byte(secret.Value)
		}
	}

	if managedTemplateData != nil {
		secretKeyValue := make(map[string]model.SecretTemplateOptions)
		for _, secret := range secretsFromAPI {
			secretKeyValue[secret.Key] = model.SecretTemplateOptions{
				Value:      secret.Value,
				SecretPath: secret.SecretPath,
			}
		}

		for templateKey, userTemplate := range managedTemplateData.Data {
			tmpl, err := tpl.New("secret-templates").Funcs(template.GetTemplateFunctions()).Parse(userTemplate)
			if err != nil {
				return fmt.Errorf("unable to compile template: %s [err=%v]", templateKey, err)
			}

			buf := bytes.NewBuffer(nil)
			err = tmpl.Execute(buf, secretKeyValue)
			if err != nil {
				return fmt.Errorf("unable to execute template: %s [err=%v]", templateKey, err)
			}
			plainProcessedSecrets[templateKey] = buf.Bytes()
		}
	}

	// Sync labels and annotations from CRD (removes ones no longer in CRD)
	newAnnotations, newLabels := r.syncLabelsAndAnnotations(infisicalSecret, managedConfigMap.ObjectMeta.Annotations, managedConfigMap.ObjectMeta.Labels)

	managedConfigMap.ObjectMeta.Labels = newLabels
	managedConfigMap.ObjectMeta.Annotations = newAnnotations
	managedConfigMap.Data = convertBinaryToStringMap(plainProcessedSecrets)
	managedConfigMap.ObjectMeta.Annotations[constants.SECRET_VERSION_ANNOTATION] = ETag

	err := r.Client.Update(ctx, &managedConfigMap)
	if err != nil {
		return fmt.Errorf("unable to update Kubernetes config map because [%w]", err)
	}

	logger.Info("successfully updated managed Kubernetes config map")
	return nil
}

func (r *InfisicalSecretReconciler) fetchSecretsFromAPI(ctx context.Context, logger logr.Logger, authDetails util.AuthenticationDetails, infisicalClient infisicalSdk.InfisicalClientInterface, infisicalSecret v1alpha1.InfisicalSecret) ([]model.SingleEnvironmentVariable, error) {

	if authDetails.AuthStrategy == util.AuthStrategy.SERVICE_ACCOUNT { // Service Account // ! Legacy auth method
		serviceAccountCreds, err := r.getInfisicalServiceAccountCredentialsFromKubeSecret(ctx, infisicalSecret)
		if err != nil {
			return nil, fmt.Errorf("ReconcileInfisicalSecret: unable to get service account creds from kube secret [err=%s]", err)
		}

		plainTextSecretsFromApi, err := util.GetPlainTextSecretsViaServiceAccount(infisicalClient, serviceAccountCreds, infisicalSecret.Spec.Authentication.ServiceAccount.ProjectId, infisicalSecret.Spec.Authentication.ServiceAccount.EnvironmentName)
		if err != nil {
			return nil, fmt.Errorf("\nfailed to get secrets because [err=%v]", err)
		}

		logger.Info("ReconcileInfisicalSecret: Fetched secrets via service account")

		return plainTextSecretsFromApi, nil

	} else if authDetails.AuthStrategy == util.AuthStrategy.SERVICE_TOKEN { // Service Tokens // ! Legacy / Deprecated auth method
		infisicalToken, err := r.getInfisicalTokenFromKubeSecret(ctx, infisicalSecret)
		if err != nil {
			return nil, fmt.Errorf("ReconcileInfisicalSecret: unable to get service token from kube secret [err=%s]", err)
		}

		envSlug := infisicalSecret.Spec.Authentication.ServiceToken.SecretsScope.EnvSlug
		secretsPath := infisicalSecret.Spec.Authentication.ServiceToken.SecretsScope.SecretsPath
		recursive := infisicalSecret.Spec.Authentication.ServiceToken.SecretsScope.Recursive

		plainTextSecretsFromApi, err := util.GetPlainTextSecretsViaServiceToken(infisicalClient, infisicalToken, envSlug, secretsPath, recursive)
		if err != nil {
			return nil, fmt.Errorf("\nfailed to get secrets because [err=%v]", err)
		}

		logger.Info("ReconcileInfisicalSecret: Fetched secrets via [type=SERVICE_TOKEN]")

		return plainTextSecretsFromApi, nil

	} else if authDetails.IsMachineIdentityAuth { // * Machine Identity authentication, the SDK will be authenticated at this point
		if err := authDetails.MachineIdentityScope.ValidateScope(); err != nil {
			return nil, fmt.Errorf("invalid machine identity scope [err=%s]", err)
		}

		if authDetails.MachineIdentityScope.ProjectSlug != "" {
			projectId, err := util.ExtractProjectIdFromSlug(infisicalClient.Auth().GetAccessToken(), authDetails.MachineIdentityScope.ProjectSlug)

			logger.Info(fmt.Sprintf("ReconcileInfisicalSecret: Extracted project id from slug [projectId=%s] [projectSlug=%s]", projectId, authDetails.MachineIdentityScope.ProjectSlug))
			if err != nil {
				return nil, fmt.Errorf("unable to extract project id from slug [err=%s]", err)
			}

			authDetails.MachineIdentityScope.ProjectID = projectId
		}

		plainTextSecretsFromApi, err := util.GetPlainTextSecretsViaMachineIdentity(infisicalClient, authDetails.MachineIdentityScope)

		if err != nil {
			return nil, fmt.Errorf("\nfailed to get secrets because [err=%v]", err)
		}

		if authDetails.MachineIdentityScope.SecretName != "" {
			logger.Info(fmt.Sprintf("ReconcileInfisicalSecret: Fetched secret via machine identity [type=%v] [secretName=%s]", authDetails.AuthStrategy, authDetails.MachineIdentityScope.SecretName))
		} else {
			logger.Info(fmt.Sprintf("ReconcileInfisicalSecret: Fetched secrets via machine identity [type=%v]", authDetails.AuthStrategy))
		}
		return plainTextSecretsFromApi, nil

	} else {
		return nil, errors.New("no authentication method provided. Please configure a authentication method then try again")
	}
}

func (r *InfisicalSecretReconciler) getResourceVariables(infisicalSecret v1alpha1.InfisicalSecret, resourceVariablesMap map[string]util.ResourceVariables) util.ResourceVariables {

	var resourceVariables util.ResourceVariables

	if _, ok := resourceVariablesMap[string(infisicalSecret.UID)]; !ok {

		ctx, cancel := context.WithCancel(context.Background())

		client := infisicalSdk.NewInfisicalClient(ctx, infisicalSdk.Config{
			SiteUrl:       config.API_HOST_URL,
			CaCertificate: config.API_CA_CERTIFICATE,
			UserAgent:     constants.USER_AGENT_NAME,
		})

		resourceVariablesMap[string(infisicalSecret.UID)] = util.ResourceVariables{
			InfisicalClient:  client,
			CancelCtx:        cancel,
			AuthDetails:      util.AuthenticationDetails{},
			ServerSentEvents: sse.NewConnectionRegistry(ctx),
		}

		resourceVariables = resourceVariablesMap[string(infisicalSecret.UID)]

	} else {
		resourceVariables = resourceVariablesMap[string(infisicalSecret.UID)]
	}

	return resourceVariables
}

func (r *InfisicalSecretReconciler) updateResourceVariables(infisicalSecret v1alpha1.InfisicalSecret, resourceVariables util.ResourceVariables, resourceVariablesMap map[string]util.ResourceVariables) {
	resourceVariablesMap[string(infisicalSecret.UID)] = resourceVariables
}

func isOwnedByInfisicalSecret(obj client.Object, infisicalSecretUID types.UID) bool {
	for _, ownerRef := range obj.GetOwnerReferences() {
		if ownerRef.UID == infisicalSecretUID &&
			ownerRef.Kind == constants.INFISICAL_SECRET_KIND {
			return true
		}
	}
	return false
}

// Removes secrets and configmaps that are owned by the InfisicalSecret but are no longer referenced in the spec
// Best effort, don't fail reconciliation
func (r *InfisicalSecretReconciler) deleteUnreferencedOwnedResources(
	ctx context.Context,
	logger logr.Logger,
	infisicalSecret v1alpha1.InfisicalSecret,
	secretOwnerReferences map[string]bool,
	configMapOwnerReferences map[string]bool,
) {
	secretList := &corev1.SecretList{}
	if err := r.List(ctx, secretList, client.InNamespace(infisicalSecret.Namespace)); err != nil {
		logger.Error(err, "Failed to list secrets for cleanup")
		return
	}

	for _, secret := range secretList.Items {
		if isOwnedByInfisicalSecret(&secret, infisicalSecret.UID) {
			key := secret.Namespace + "/" + secret.Name
			if !secretOwnerReferences[key] {
				logger.Info("Deleting orphaned owned secret", "secret", key)
				if err := r.Delete(ctx, &secret); err != nil {
					logger.Error(err, "Failed to delete orphaned owned secret", "secret", key)
				}
			}
		}
	}

	configMapList := &corev1.ConfigMapList{}
	if err := r.List(ctx, configMapList, client.InNamespace(infisicalSecret.Namespace)); err != nil {
		logger.Error(err, "Failed to list configmaps for cleanup")
		return
	}

	for _, cm := range configMapList.Items {
		if isOwnedByInfisicalSecret(&cm, infisicalSecret.UID) {
			key := cm.Namespace + "/" + cm.Name
			if !configMapOwnerReferences[key] {
				logger.Info("Deleting orphaned owned configmap", "configmap", key)
				if err := r.Delete(ctx, &cm); err != nil {
					logger.Error(err, "Failed to delete orphaned owned configmap", "configmap", key)
				}
			}
		}
	}
}

func (r *InfisicalSecretReconciler) ReconcileInfisicalSecret(ctx context.Context, logger logr.Logger, infisicalSecret *v1alpha1.InfisicalSecret, managedKubeSecretReferences []v1alpha1.ManagedKubeSecretConfig, managedKubeConfigMapReferences []v1alpha1.ManagedKubeConfigMapConfig, resourceVariablesMap map[string]util.ResourceVariables) (int, error) {

	if infisicalSecret == nil {
		return 0, fmt.Errorf("infisicalSecret is nil")
	}

	resourceVariables := r.getResourceVariables(*infisicalSecret, resourceVariablesMap)
	infisicalClient := resourceVariables.InfisicalClient
	cancelCtx := resourceVariables.CancelCtx
	authDetails := resourceVariables.AuthDetails
	var err error

	if authDetails.AuthStrategy == "" {
		logger.Info("No authentication strategy found. Attempting to authenticate")
		authDetails, err = util.HandleAuthentication(ctx, util.SecretAuthInput{
			Secret: *infisicalSecret,
			Type:   util.SecretCrd.INFISICAL_SECRET,
		}, r.Client, infisicalClient, r.IsNamespaceScoped)

		r.SetInfisicalTokenLoadCondition(ctx, logger, infisicalSecret, authDetails.AuthStrategy, err)

		if err != nil {
			return 0, fmt.Errorf("unable to authenticate [err=%s]", err)
		}

		r.updateResourceVariables(*infisicalSecret, util.ResourceVariables{
			InfisicalClient:  infisicalClient,
			CancelCtx:        cancelCtx,
			AuthDetails:      authDetails,
			ServerSentEvents: sse.NewConnectionRegistry(ctx),
		}, resourceVariablesMap)
	}

	plainTextSecretsFromApi, err := r.fetchSecretsFromAPI(ctx, logger, authDetails, infisicalClient, *infisicalSecret)

	if err != nil {
		return 0, fmt.Errorf("failed to fetch secrets from API for managed secrets [err=%s]", err)
	}
	secretsCount := len(plainTextSecretsFromApi)
	secretOwnerReferences := make(map[string]bool)

	if len(managedKubeSecretReferences) > 0 {
		for _, managedSecretReference := range managedKubeSecretReferences {
			if managedSecretReference.CreationPolicy == "Owner" {
				key := managedSecretReference.SecretNamespace + "/" + managedSecretReference.SecretName
				secretOwnerReferences[key] = true
			}
			// Look for managed secret by name and namespace
			managedKubeSecret, err := util.GetKubeSecretByNamespacedName(ctx, r.Client, types.NamespacedName{
				Name:      managedSecretReference.SecretName,
				Namespace: managedSecretReference.SecretNamespace,
			})

			if err != nil && !k8Errors.IsNotFound(err) {
				if util.IsNamespaceScopedError(err, r.IsNamespaceScoped) {
					return 0, fmt.Errorf("unable to fetch Kubernetes secret. Your Operator installation is namespace scoped, and cannot read secrets outside of the namespace it is installed in. Please ensure the secret is in the same namespace as the operator. [err=%v]", err)
				}
				return 0, fmt.Errorf("something went wrong when fetching the managed Kubernetes secret [%w]", err)
			}

			newEtag := crypto.ComputeEtag([]byte(fmt.Sprintf("%v", plainTextSecretsFromApi)))
			if managedKubeSecret == nil {
				if err := r.createInfisicalManagedKubeResource(ctx, logger, *infisicalSecret, managedSecretReference, plainTextSecretsFromApi, newEtag, constants.MANAGED_KUBE_RESOURCE_TYPE_SECRET); err != nil {
					return 0, fmt.Errorf("failed to create managed secret [err=%s]", err)
				}
			} else {
				if err := r.updateInfisicalManagedKubeSecret(ctx, logger, *infisicalSecret, managedSecretReference, *managedKubeSecret, plainTextSecretsFromApi, newEtag); err != nil {
					return 0, fmt.Errorf("failed to update managed secret [err=%s]", err)
				}
			}
		}
	}

	configMapOwnerReferences := make(map[string]bool)

	if len(managedKubeConfigMapReferences) > 0 {
		for _, managedConfigMapReference := range managedKubeConfigMapReferences {
			if managedConfigMapReference.CreationPolicy == "Owner" {
				key := managedConfigMapReference.ConfigMapNamespace + "/" + managedConfigMapReference.ConfigMapName
				configMapOwnerReferences[key] = true
			}

			managedKubeConfigMap, err := util.GetKubeConfigMapByNamespacedName(ctx, r.Client, types.NamespacedName{
				Name:      managedConfigMapReference.ConfigMapName,
				Namespace: managedConfigMapReference.ConfigMapNamespace,
			})

			if err != nil && !k8Errors.IsNotFound(err) {
				if util.IsNamespaceScopedError(err, r.IsNamespaceScoped) {
					return 0, fmt.Errorf("unable to fetch Kubernetes config map. Your Operator installation is namespace scoped, and cannot read config maps outside of the namespace it is installed in. Please ensure the config map is in the same namespace as the operator. [err=%v]", err)
				}
				return 0, fmt.Errorf("something went wrong when fetching the managed Kubernetes config map [%w]", err)
			}

			newEtag := crypto.ComputeEtag([]byte(fmt.Sprintf("%v", plainTextSecretsFromApi)))
			if managedKubeConfigMap == nil {
				if err := r.createInfisicalManagedKubeResource(ctx, logger, *infisicalSecret, managedConfigMapReference, plainTextSecretsFromApi, newEtag, constants.MANAGED_KUBE_RESOURCE_TYPE_CONFIG_MAP); err != nil {
					return 0, fmt.Errorf("failed to create managed config map [err=%s]", err)
				}
			} else {
				if err := r.updateInfisicalManagedConfigMap(ctx, logger, *infisicalSecret, managedConfigMapReference, *managedKubeConfigMap, plainTextSecretsFromApi, newEtag); err != nil {
					return 0, fmt.Errorf("failed to update managed config map [err=%s]", err)
				}
			}

		}
	}

	r.deleteUnreferencedOwnedResources(ctx, logger, *infisicalSecret, secretOwnerReferences, configMapOwnerReferences)

	return secretsCount, nil
}

func (r *InfisicalSecretReconciler) CloseInstantUpdatesStream(ctx context.Context, logger logr.Logger, infisicalSecret *v1alpha1.InfisicalSecret, resourceVariablesMap map[string]util.ResourceVariables) error {
	if infisicalSecret == nil {
		return fmt.Errorf("infisicalSecret is nil")
	}

	variables := r.getResourceVariables(*infisicalSecret, resourceVariablesMap)

	if !variables.AuthDetails.IsMachineIdentityAuth {
		return fmt.Errorf("only machine identity is supported for subscriptions")
	}

	conn := variables.ServerSentEvents

	if _, ok := conn.Get(); ok {
		conn.Close()
	}

	return nil
}

func (r *InfisicalSecretReconciler) OpenInstantUpdatesStream(ctx context.Context, logger logr.Logger, infisicalSecret *v1alpha1.InfisicalSecret, resourceVariablesMap map[string]util.ResourceVariables, eventCh chan<- event.TypedGenericEvent[client.Object]) error {
	if infisicalSecret == nil {
		return fmt.Errorf("infisicalSecret is nil")
	}

	variables := r.getResourceVariables(*infisicalSecret, resourceVariablesMap)

	if !variables.AuthDetails.IsMachineIdentityAuth {
		return fmt.Errorf("only machine identity is supported for subscriptions")
	}

	identityScope := variables.AuthDetails.MachineIdentityScope

	if err := identityScope.ValidateScope(); err != nil {
		return fmt.Errorf("invalid machine identity scope [err=%s]", err)
	}

	infisicalClient := variables.InfisicalClient
	sseRegistry := variables.ServerSentEvents

	token := infisicalClient.Auth().GetAccessToken()

	if identityScope.ProjectSlug != "" {

		projectId, err := util.ExtractProjectIdFromSlug(infisicalClient.Auth().GetAccessToken(), identityScope.ProjectSlug)
		if err != nil {
			return fmt.Errorf("unable to extract project id from slug [err=%s]", err)
		}

		logger.Info(fmt.Sprintf("OpenInstantUpdatesStream: Extracted project id from slug [projectId=%s] [projectSlug=%s]", projectId, identityScope.ProjectSlug))
		identityScope.ProjectID = projectId
	}

	if identityScope.Recursive {
		identityScope.SecretsPath = fmt.Sprint(identityScope.SecretsPath, "**")
	}

	events, errors, err := sseRegistry.Subscribe(func() (*http.Response, error) {

		httpClient, err := util.CreateRestyClient(model.CreateRestyClientOptions{
			AccessToken: token,
			Headers: map[string]string{
				"Content-Type": "application/json",
				"Accept":       "text/event-stream",
				"Connection":   "keep-alive",
			},
		})

		if err != nil {
			return nil, fmt.Errorf("unable to create resty client. [err=%v]", err)
		}

		req, err := api.CallSubscribeProjectEvents(httpClient, identityScope.ProjectID, identityScope.SecretsPath, identityScope.EnvSlug)

		if err != nil {
			return nil, err
		}

		return req, nil
	})

	if err != nil {
		return fmt.Errorf("unable to connect sse [err=%s]", err)
	}

	go func() {
	outer:
		for {
			select {
			case ev := <-events:
				logger.Info("Received SSE Event", "event", ev)
				eventCh <- event.TypedGenericEvent[client.Object]{
					Object: infisicalSecret,
				}
			case err := <-errors:
				logger.Error(err, "Error occurred")
				break outer
			case <-ctx.Done():
				break outer
			}
		}
	}()

	return nil
}

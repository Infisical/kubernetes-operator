package infisicalstaticsecretpush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/go-logr/logr"
	"github.com/go-resty/resty/v2"
	k8Errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/services/infisicalsession"
	"github.com/Infisical/infisical/k8-operator/internal/util"
)

// maxBatchBytes keeps each batch request well under the 1 MB request body limit of the Infisical API.
const maxBatchBytes = 512 * 1024

const secretTypePersonal = "personal"

type InfisicalStaticSecretPushReconciler struct {
	client.Client
	IsNamespaceScoped bool
	session           *infisicalsession.Resolver
	logger            logr.Logger
}

// secretRef identifies a secret by its folder and key.
type secretRef struct {
	location api.SecretLocation
	key      string
}

// targetResult is what syncing one target produced.
type targetResult struct {
	location api.SecretLocation
	// owned holds the ID and key of every secret this resource owns at the target
	// after the sync, whether it was pushed now or was already up to date.
	owned map[string]string
	// listed holds where every secret found at the target lives, keyed by ID, so
	// cleanup can locate stale secrets without looking each one up.
	listed map[string]secretRef
}

// Validate checks the parts of the spec that the CRD schema can't express.
func (r *InfisicalStaticSecretPushReconciler) Validate(push *v1beta1.InfisicalStaticSecretPush) error {
	if push == nil {
		return model.ErrInvalidStaticSecretPushObject
	}

	spec := &push.Spec

	if spec.SyncOptions.RefreshInterval != "" {
		if _, err := util.ConvertResyncIntervalToDuration(spec.SyncOptions.RefreshInterval, false); err != nil {
			return fmt.Errorf("invalid refreshInterval %q: %w", spec.SyncOptions.RefreshInterval, err)
		}
	}

	if len(spec.Sources.Secrets) == 0 {
		return fmt.Errorf("at least one source secret is required")
	}

	sources := make(map[string]struct{}, len(spec.Sources.Secrets))
	for i, source := range spec.Sources.Secrets {
		if source.Name == "" {
			return fmt.Errorf("sources.secrets[%d].name is required", i)
		}
		if source.Namespace == "" {
			return fmt.Errorf("sources.secrets[%d].namespace is required", i)
		}

		key := source.Namespace + "/" + source.Name
		if _, exists := sources[key]; exists {
			return fmt.Errorf("duplicate source secret %q", key)
		}
		sources[key] = struct{}{}
	}

	if len(spec.Targets) == 0 {
		return fmt.Errorf("at least one target is required")
	}

	targets := make(map[string]struct{}, len(spec.Targets))
	for i, target := range spec.Targets {
		if target.ProjectId == "" && target.ProjectSlug == "" {
			return fmt.Errorf("either targets[%d].projectId or targets[%d].projectSlug must be set", i, i)
		}
		if target.ProjectId != "" && target.ProjectSlug != "" {
			return fmt.Errorf("you declared both targets[%d].projectId and targets[%d].projectSlug: you should declare only one", i, i)
		}
		if target.EnvironmentSlug == "" {
			return fmt.Errorf("targets[%d].environmentSlug is required", i)
		}
		if !strings.HasPrefix(target.SecretPath, "/") {
			return fmt.Errorf("targets[%d].secretPath must start with /", i)
		}

		key := fmt.Sprintf("%s%s/%s:%s", target.ProjectId, target.ProjectSlug, target.EnvironmentSlug, normalizeSecretPath(target.SecretPath))
		if _, exists := targets[key]; exists {
			return fmt.Errorf("targets[%d] duplicates another target", i)
		}
		targets[key] = struct{}{}
	}

	return nil
}

// Authenticate logs in through the InfisicalAuth the resource references and records
// the auth method on the status as soon as the InfisicalAuth is found.
func (r *InfisicalStaticSecretPushReconciler) Authenticate(ctx context.Context, push *v1beta1.InfisicalStaticSecretPush) (infisicalsession.Session, error) {
	session, err := r.session.Authenticate(ctx, push.Spec.InfisicalAuthRef)
	if session.Auth != nil {
		setAuthMethodCondition(push, string(session.Auth.Spec.Method))
	}
	return session, err
}

// ReadSources reads every source Secret and merges their keys. When several Secrets
// contain the same key, the Secret listed first wins. A missing Secret fails the whole
// read, so it is never mistaken for a Secret whose keys were all removed.
func (r *InfisicalStaticSecretPushReconciler) ReadSources(ctx context.Context, push *v1beta1.InfisicalStaticSecretPush) (map[string]string, error) {
	merged := make(map[string]string)

	for _, source := range push.Spec.Sources.Secrets {
		name := source.Namespace + "/" + source.Name

		secret, err := util.GetKubeSecretByNamespacedName(ctx, r.Client, types.NamespacedName{Name: source.Name, Namespace: source.Namespace})
		if err != nil {
			if util.IsNamespaceScopedError(err, r.IsNamespaceScoped) {
				return nil, model.NewNamespaceScopedError(err, fmt.Sprintf("Secret %s", name))
			}
			if k8Errors.IsNotFound(err) {
				return nil, fmt.Errorf("source Secret %s not found", name)
			}
			return nil, fmt.Errorf("unable to fetch source Secret %s: %w", name, err)
		}

		// Infisical stores text, and invalid UTF-8 would be silently replaced while
		// encoding the request, so binary values are rejected instead of corrupted.
		var invalidKeys []string
		for key, value := range secret.Data {
			if !utf8.Valid(value) {
				invalidKeys = append(invalidKeys, key)
			}
		}
		if len(invalidKeys) > 0 {
			slices.Sort(invalidKeys)
			return nil, fmt.Errorf("source Secret %s has values that are not valid UTF-8 in keys [%s]: Infisical stores text, so base64-encode binary values in the source Secret", name, summarize(invalidKeys))
		}

		for key, value := range secret.Data {
			if _, exists := merged[key]; !exists {
				merged[key] = string(value)
			}
		}
	}

	return merged, nil
}

// PushToTargets pushes the desired secrets to every target and deletes the secrets this
// resource pushed earlier but no longer pushes. It updates status.managedSecrets with
// everything the resource owns afterwards. A failing target doesn't stop the others.
func (r *InfisicalStaticSecretPushReconciler) PushToTargets(ctx context.Context, restClient *resty.Client, session infisicalsession.Session, push *v1beta1.InfisicalStaticSecretPush, desired map[string]string) error {
	previouslyManaged := push.Status.ManagedSecrets
	owned := make(map[string]string)
	listed := make(map[string]secretRef)
	ownedRefs := make(map[secretRef]struct{})
	targetIndexByLocation := make(map[api.SecretLocation]int, len(push.Spec.Targets))
	otherOwners := r.otherOwners(ctx, push)

	var targetErrs []error
	for i, target := range push.Spec.Targets {
		result, err := r.syncTarget(restClient, push, target, desired, otherOwners, targetIndexByLocation, i)

		maps.Copy(owned, result.owned)
		maps.Copy(listed, result.listed)
		for _, key := range result.owned {
			ownedRefs[secretRef{location: result.location, key: key}] = struct{}{}
		}

		if err != nil {
			r.session.InvalidateOnUnauthorized(session, err)
			targetErrs = append(targetErrs, fmt.Errorf("%s: %w", describeTarget(i, target), err))
		}
	}

	if len(targetErrs) > 0 {
		// A failed target returns no IDs, so its secrets would look stale. Keep every
		// recorded ID until all targets succeed, and record the new ones too.
		merged := maps.Clone(previouslyManaged)
		if merged == nil {
			merged = make(map[string]string, len(owned))
		}
		maps.Copy(merged, owned)
		push.Status.ManagedSecrets = nilIfEmpty(merged)
		return errors.Join(targetErrs...)
	}

	stale := make(map[string]string)
	for id, key := range previouslyManaged {
		if _, stillOwned := owned[id]; !stillOwned {
			stale[id] = key
		}
	}

	remaining := map[string]string{}
	var cleanupErr error
	if len(stale) > 0 {
		if push.Spec.SyncOptions.GetDeletionPolicy() == v1beta1.PushDeletionPolicyDelete {
			remaining, cleanupErr = r.deleteSecrets(restClient, stale, listed, ownedRefs, otherOwners)
		} else {
			r.logger.Info(fmt.Sprintf("Deletion policy is None, no longer tracking %d secrets that are not pushed anymore", len(stale)))
		}
	}

	maps.Copy(remaining, owned)
	push.Status.ManagedSecrets = nilIfEmpty(remaining)

	if cleanupErr != nil {
		r.session.InvalidateOnUnauthorized(session, cleanupErr)
		return fmt.Errorf("unable to delete secrets that are no longer pushed: %w", cleanupErr)
	}

	return nil
}

// DeleteManagedSecrets deletes every secret recorded in status.managedSecrets and keeps
// the ones it couldn't delete recorded, so a retry only repeats what failed.
func (r *InfisicalStaticSecretPushReconciler) DeleteManagedSecrets(ctx context.Context, restClient *resty.Client, push *v1beta1.InfisicalStaticSecretPush) error {
	managed := push.Status.ManagedSecrets
	if len(managed) == 0 {
		return nil
	}

	// Listing the configured targets locates most secrets in one request per target.
	// Secrets at targets that were removed earlier are looked up by ID instead.
	listed := make(map[string]secretRef)
	for i, target := range push.Spec.Targets {
		location, err := r.resolveLocation(restClient, push, target)
		if err == nil {
			var existing []api.Secret
			existing, err = r.listSecrets(restClient, location)
			for _, secret := range existing {
				listed[secret.ID] = secretRef{location: location, key: secret.SecretKey}
			}
		}
		if err != nil {
			r.logger.Error(err, "Unable to list target, looking up its secrets by ID instead", "target", describeTarget(i, target))
		}
	}

	remaining, err := r.deleteSecrets(restClient, managed, listed, nil, r.otherOwners(ctx, push))
	push.Status.ManagedSecrets = nilIfEmpty(remaining)
	return err
}

func (r *InfisicalStaticSecretPushReconciler) resolveLocation(restClient *resty.Client, push *v1beta1.InfisicalStaticSecretPush, target v1beta1.PushTarget) (api.SecretLocation, error) {
	projectID, err := r.session.ResolveProjectID(restClient, push.Spec.InfisicalAuthRef, target.ProjectId, target.ProjectSlug)
	if err != nil {
		return api.SecretLocation{}, fmt.Errorf("unable to resolve project: %w", err)
	}

	return api.SecretLocation{
		ProjectID:   projectID,
		Environment: target.EnvironmentSlug,
		SecretPath:  normalizeSecretPath(target.SecretPath),
	}, nil
}

func (r *InfisicalStaticSecretPushReconciler) syncTarget(
	restClient *resty.Client,
	push *v1beta1.InfisicalStaticSecretPush,
	target v1beta1.PushTarget,
	desired map[string]string,
	otherOwners func() (map[string]otherOwner, error),
	targetIndexByLocation map[api.SecretLocation]int,
	index int,
) (targetResult, error) {
	location, err := r.resolveLocation(restClient, push, target)
	if err != nil {
		return targetResult{}, err
	}
	result := targetResult{location: location, owned: map[string]string{}, listed: map[string]secretRef{}}

	// A projectSlug and a projectId can point to the same project, which validation can't catch.
	if other, exists := targetIndexByLocation[location]; exists {
		return result, fmt.Errorf("points to the same project, environment and path as targets[%d]", other)
	}
	targetIndexByLocation[location] = index

	existing, err := r.listSecrets(restClient, location)
	if err != nil {
		return result, err
	}
	for _, secret := range existing {
		result.listed[secret.ID] = secretRef{location: location, key: secret.SecretKey}
	}

	plan := planTarget(desired, existing, push.Status.ManagedSecrets, push.Spec.SyncOptions.GetUpdatePolicy())

	var conflictErr error
	if len(plan.claims) > 0 {
		owners, err := otherOwners()
		if err != nil {
			return result, err
		}
		if conflicts := plan.dropClaimsOwnedByOthers(owners); len(conflicts) > 0 {
			conflictErr = conflictError(conflicts)
		}
	}
	maps.Copy(result.owned, plan.owned)

	if len(plan.keptByPolicy) > 0 {
		r.logger.Info(fmt.Sprintf("Left %d existing secrets at %s:%s unchanged because updatePolicy is None: %s",
			len(plan.keptByPolicy), location.Environment, location.SecretPath, summarize(plan.keptByPolicy)))
	}

	if len(plan.upserts) == 0 {
		return result, conflictErr
	}

	stored, err := r.upsertSecrets(restClient, location, plan.upserts, push.Spec.SyncOptions.ShouldCreateMissingFolders())
	for _, secret := range stored {
		result.owned[secret.ID] = secret.SecretKey
	}
	if err != nil {
		return result, errors.Join(err, conflictErr)
	}

	r.logger.Info(fmt.Sprintf("Pushed %d secrets to %s:%s", len(stored), location.Environment, location.SecretPath))
	return result, conflictErr
}

// listSecrets returns the shared secrets stored directly in a folder, with secret
// references unexpanded so they compare equal to the pushed values. A missing folder
// has no secrets.
func (r *InfisicalStaticSecretPushReconciler) listSecrets(restClient *resty.Client, location api.SecretLocation) ([]api.Secret, error) {
	response, err := api.ListSecrets(restClient, api.ListSecretsRequest{
		ProjectId:              location.ProjectID,
		EnvironmentSlug:        location.Environment,
		SecretPath:             location.SecretPath,
		ExcludeImports:         true,
		SkipReferenceExpansion: true,
	})
	if api.IsFolderNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	secrets := make([]api.Secret, 0, len(response.Secrets))
	for _, secret := range response.Secrets {
		if secret.Type != secretTypePersonal {
			secrets = append(secrets, secret)
		}
	}
	return secrets, nil
}

// targetPlan is what a sync must change at one target.
type targetPlan struct {
	upserts []api.UpsertSecret
	// owned holds the secrets this resource owns that it doesn't write, keyed by ID.
	owned map[string]string
	// claims maps the key of every existing secret the plan overwrites or takes over to
	// that secret's ID.
	claims map[string]string
	// keptByPolicy holds the keys whose value in Infisical differs from the source but
	// that are left unchanged because the update policy is None.
	keptByPolicy []string
}

// planTarget decides which desired secrets must be written to a target. Missing keys
// are always created. With PushUpdatePolicyReplace, existing keys whose value differs
// are overwritten, and existing keys this resource doesn't own yet are taken over. With
// PushUpdatePolicyNone, existing keys are never changed, including the ones this
// resource created; it keeps owning those, so they are still deleted with it.
func planTarget(desired map[string]string, existing []api.Secret, managed map[string]string, updatePolicy v1beta1.PushUpdatePolicy) targetPlan {
	existingByKey := make(map[string]api.Secret, len(existing))
	for _, secret := range existing {
		existingByKey[secret.SecretKey] = secret
	}

	plan := targetPlan{owned: map[string]string{}, claims: map[string]string{}}
	for _, key := range slices.Sorted(maps.Keys(desired)) {
		value := desired[key]

		current, exists := existingByKey[key]
		if !exists {
			plan.upserts = append(plan.upserts, api.UpsertSecret{SecretKey: key, SecretValue: value})
			continue
		}

		_, isOwned := managed[current.ID]
		upToDate := normalizeSecretValue(value) == current.SecretValue

		if updatePolicy == v1beta1.PushUpdatePolicyNone {
			if isOwned {
				plan.owned[current.ID] = key
			}
			if !upToDate {
				plan.keptByPolicy = append(plan.keptByPolicy, key)
			}
			continue
		}

		if !upToDate {
			plan.upserts = append(plan.upserts, api.UpsertSecret{SecretKey: key, SecretValue: value})
			plan.claims[key] = current.ID
			continue
		}

		if !isOwned {
			plan.claims[key] = current.ID
		}
		plan.owned[current.ID] = key
	}

	return plan
}

// dropClaimsOwnedByOthers removes the overwrites and take-overs of secrets that another
// InfisicalStaticSecretPush owns, given the owner of each secret ID, and returns the
// keys it removed mapped to their owner. An owner that is being deleted still counts:
// its pending cleanup would delete a key right after this resource claimed it.
func (p *targetPlan) dropClaimsOwnedByOthers(owners map[string]otherOwner) map[string]otherOwner {
	conflicts := make(map[string]otherOwner)
	for key, id := range p.claims {
		if owner, ownedByOther := owners[id]; ownedByOther {
			conflicts[key] = owner
			delete(p.owned, id)
		}
	}
	if len(conflicts) == 0 {
		return nil
	}

	p.upserts = slices.DeleteFunc(p.upserts, func(upsert api.UpsertSecret) bool {
		_, conflicting := conflicts[upsert.SecretKey]
		return conflicting
	})
	return conflicts
}

// otherOwner is another InfisicalStaticSecretPush that recorded a secret.
type otherOwner struct {
	name     string
	deleting bool
}

func (o otherOwner) String() string {
	if o.deleting {
		return o.name + ", which is being deleted"
	}
	return o.name
}

// otherOwners returns a function that maps the ID of every secret recorded by another
// InfisicalStaticSecretPush to that resource. Two resources pushing the same key would
// overwrite each other on every sync, and deleting one would delete the key the other
// pushed, so the operator leaves such keys alone. The resources are listed at most once,
// and only when a sync is about to overwrite, take over or delete an existing secret.
func (r *InfisicalStaticSecretPushReconciler) otherOwners(ctx context.Context, push *v1beta1.InfisicalStaticSecretPush) func() (map[string]otherOwner, error) {
	return sync.OnceValues(func() (map[string]otherOwner, error) {
		var pushes v1beta1.InfisicalStaticSecretPushList
		if err := r.List(ctx, &pushes, client.UnsafeDisableDeepCopy); err != nil {
			return nil, fmt.Errorf("unable to list InfisicalStaticSecretPushes to find keys pushed by another resource: %w", err)
		}

		owners := make(map[string]otherOwner)
		for _, other := range pushes.Items {
			if other.Namespace == push.Namespace && other.Name == push.Name {
				continue
			}
			owner := otherOwner{name: other.Namespace + "/" + other.Name, deleting: !other.DeletionTimestamp.IsZero()}
			for id := range other.Status.ManagedSecrets {
				owners[id] = owner
			}
		}
		return owners, nil
	})
}

func conflictError(conflicts map[string]otherOwner) error {
	described := make([]string, 0, len(conflicts))
	for _, key := range slices.Sorted(maps.Keys(conflicts)) {
		described = append(described, fmt.Sprintf("%s (pushed by %s)", key, conflicts[key]))
	}
	return fmt.Errorf("keys already pushed by another InfisicalStaticSecretPush were left unchanged: %s: push each key from only one resource", summarize(described))
}

// maxListedItems bounds how many keys a message names, so it stays readable and fits
// in a status condition however many keys are affected.
const maxListedItems = 10

// summarize joins items for a message, naming at most maxListedItems of them.
func summarize(items []string) string {
	if len(items) <= maxListedItems {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(items[:maxListedItems], ", "), len(items)-maxListedItems)
}

func (r *InfisicalStaticSecretPushReconciler) upsertSecrets(restClient *resty.Client, location api.SecretLocation, secrets []api.UpsertSecret, createMissingFolders bool) ([]api.Secret, error) {
	var stored []api.Secret
	for _, batch := range chunkByJSONSize(secrets, maxBatchBytes) {
		result, err := r.upsertBatch(restClient, location, batch, createMissingFolders)
		stored = append(stored, result...)
		if err != nil {
			return stored, err
		}
	}
	return stored, nil
}

func (r *InfisicalStaticSecretPushReconciler) upsertBatch(restClient *resty.Client, location api.SecretLocation, batch []api.UpsertSecret, createMissingFolders bool) ([]api.Secret, error) {
	request := api.UpsertSecretsRequest{SecretLocation: location, Secrets: batch}

	stored, err := api.UpsertSecrets(restClient, request)
	if api.IsFolderNotFound(err) && location.SecretPath != "/" {
		if !createMissingFolders {
			return nil, fmt.Errorf("folder %s does not exist in environment %s: create it, or set syncOptions.createMissingFolders to true", location.SecretPath, location.Environment)
		}

		if err := api.CreateFolder(restClient, api.CreateFolderRequest{
			ProjectID:   location.ProjectID,
			Environment: location.Environment,
			Path:        path.Dir(location.SecretPath),
			Name:        path.Base(location.SecretPath),
		}); err != nil {
			return nil, fmt.Errorf("unable to create folder %s: %w", location.SecretPath, err)
		}
		r.logger.Info(fmt.Sprintf("Created folder %s in environment %s", location.SecretPath, location.Environment))

		stored, err = api.UpsertSecrets(restClient, request)
	}

	if errors.Is(err, api.ErrApprovalRequired) {
		return nil, approvalRequiredError(location)
	}
	if err != nil {
		return nil, err
	}
	return stored, nil
}

// deleteSecrets deletes the given secrets, keyed by ID, from wherever they are stored.
// Secrets found in listed are located without a request; the others are looked up by
// ID, and ones that no longer exist are skipped. A secret whose folder and key are in
// keep is not deleted, because this resource owns that key under a newer ID, and a
// secret that another InfisicalStaticSecretPush owns is left to it. It returns the
// secrets it couldn't delete.
//
// The Infisical API deletes secrets by key, not by ID. listed must therefore come from
// the same reconcile, which keeps the window in which someone could replace a key with
// a secret this resource doesn't own short.
func (r *InfisicalStaticSecretPushReconciler) deleteSecrets(
	restClient *resty.Client,
	secrets map[string]string,
	listed map[string]secretRef,
	keep map[secretRef]struct{},
	otherOwners func() (map[string]otherOwner, error),
) (map[string]string, error) {
	failed := make(map[string]string)
	byLocation := make(map[api.SecretLocation]map[string]string)
	var errs []error

	owners, err := otherOwners()
	if err != nil {
		return maps.Clone(secrets), err
	}

	for _, id := range slices.Sorted(maps.Keys(secrets)) {
		// An owner that is being deleted doesn't keep the secret: otherwise two resources
		// that recorded it and are deleted together would each leave it to the other.
		if owner, ownedByOther := owners[id]; ownedByOther && !owner.deleting {
			r.logger.Info(fmt.Sprintf("Not deleting secret %s, because InfisicalStaticSecretPush %s pushes it too", secrets[id], owner))
			continue
		}

		ref, found := listed[id]
		if !found {
			secret, err := api.GetSecretByID(restClient, id)
			if errors.Is(err, api.ErrNotFound) {
				continue
			}
			if err != nil {
				failed[id] = secrets[id]
				errs = append(errs, fmt.Errorf("unable to look up secret %s: %w", secrets[id], err))
				continue
			}
			ref = secretRef{
				location: api.SecretLocation{
					ProjectID:   secret.ProjectID,
					Environment: secret.Environment,
					SecretPath:  normalizeSecretPath(secret.SecretPath),
				},
				key: secret.SecretKey,
			}
		}

		if _, kept := keep[ref]; kept {
			continue
		}

		if byLocation[ref.location] == nil {
			byLocation[ref.location] = make(map[string]string)
		}
		byLocation[ref.location][id] = ref.key
	}

	for location, idsToKeys := range byLocation {
		if err := r.deleteAtLocation(restClient, location, idsToKeys); err != nil {
			maps.Copy(failed, idsToKeys)
			errs = append(errs, fmt.Errorf("%s:%s: %w", location.Environment, location.SecretPath, err))
			continue
		}
		r.logger.Info(fmt.Sprintf("Deleted %d secrets from %s:%s", len(idsToKeys), location.Environment, location.SecretPath))
	}

	return failed, errors.Join(errs...)
}

// deleteAtLocation deletes secrets, keyed by ID, from one folder. The batch delete fails
// as a whole when any of its secrets is already gone, so on that error the folder is
// listed again and only the secrets that still exist are retried.
func (r *InfisicalStaticSecretPushReconciler) deleteAtLocation(restClient *resty.Client, location api.SecretLocation, idsToKeys map[string]string) error {
	err := r.deleteBatches(restClient, location, slices.Sorted(maps.Values(idsToKeys)))
	if err == nil || api.IsFolderNotFound(err) {
		return nil
	}
	if !errors.Is(err, api.ErrNotFound) {
		return err
	}

	existing, listErr := r.listSecrets(restClient, location)
	if listErr != nil {
		return err
	}

	var keys []string
	for _, secret := range existing {
		if key, ours := idsToKeys[secret.ID]; ours && key == secret.SecretKey {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil
	}

	slices.Sort(keys)
	return r.deleteBatches(restClient, location, keys)
}

func (r *InfisicalStaticSecretPushReconciler) deleteBatches(restClient *resty.Client, location api.SecretLocation, keys []string) error {
	secrets := make([]api.DeleteSecret, 0, len(keys))
	for _, key := range keys {
		secrets = append(secrets, api.DeleteSecret{SecretKey: key})
	}

	for _, batch := range chunkByJSONSize(secrets, maxBatchBytes) {
		err := api.DeleteSecrets(restClient, api.DeleteSecretsRequest{SecretLocation: location, Secrets: batch})
		if errors.Is(err, api.ErrApprovalRequired) {
			return approvalRequiredError(location)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func approvalRequiredError(location api.SecretLocation) error {
	return fmt.Errorf("changes to %s:%s require approval: an approval request was created in Infisical, and the operator will take over the secrets once it is approved", location.Environment, location.SecretPath)
}

// chunkByJSONSize splits items into batches whose encoded size stays under maxBytes.
// An item larger than maxBytes on its own gets its own batch.
func chunkByJSONSize[T any](items []T, maxBytes int) [][]T {
	var batches [][]T
	var current []T
	currentSize := 0

	for _, item := range items {
		encoded, err := json.Marshal(item)
		size := len(encoded) + 1 // the separating comma
		if err != nil {
			size = maxBytes
		}

		if len(current) > 0 && currentSize+size > maxBytes {
			batches = append(batches, current)
			current = nil
			currentSize = 0
		}
		current = append(current, item)
		currentSize += size
	}

	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

// normalizeSecretValue returns the value as Infisical stores it: Infisical trims
// surrounding whitespace and keeps a single trailing newline when the value ended with
// one. Comparing normalized values keeps such values from being pushed on every sync.
func normalizeSecretValue(value string) string {
	if strings.HasSuffix(value, "\n") {
		return strings.TrimSpace(value) + "\n"
	}
	return strings.TrimSpace(value)
}

// normalizeSecretPath returns the path the way Infisical reports it, without a trailing slash.
func normalizeSecretPath(secretPath string) string {
	return path.Clean("/" + secretPath)
}

func describeTarget(index int, target v1beta1.PushTarget) string {
	project := target.ProjectId
	if target.ProjectSlug != "" {
		project = target.ProjectSlug
	}
	return fmt.Sprintf("targets[%d] (project %s, %s:%s)", index, project, target.EnvironmentSlug, normalizeSecretPath(target.SecretPath))
}

func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

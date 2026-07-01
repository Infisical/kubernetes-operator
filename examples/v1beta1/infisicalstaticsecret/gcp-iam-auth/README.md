# GCP IAM Auth - Additional Setup

For details on setting up GCP IAM auth on the Infisical side, see the [GCP IAM Auth guide](https://infisical.com/docs/documentation/platform/identities/gcp-auth#gcp-iam-auth).

GCP IAM auth requires a service account key JSON file to be available inside the operator pod. This means you need to:

1. Create a Kubernetes Secret with your GCP service account key
2. Mount it into the operator pod via Helm values

## 1. Create the Secret

Download your GCP service account key JSON file from the GCP Console, then run the following command from the same directory where the file was downloaded. Replace the namespace with the one where the operator Helm chart is installed:

```bash
kubectl create secret generic gcp-sa-key \
  --from-file=service-account-key.json=./service-account-key.json \
  -n <operator-namespace>
```

## 2. Mount it into the operator pod

Add the following to your Helm values:

```yaml
controllerManager:
  extraVolumes:
    - name: gcp-sa-key
      secret:
        secretName: gcp-sa-key
  manager:
    extraVolumeMounts:
      - name: gcp-sa-key
        mountPath: /etc/gcp
        readOnly: true
```

Then upgrade the release:

```bash
helm upgrade <release-name> infisical/secrets-operator -f values.yaml -n <operator-namespace>
```

## 3. Apply the example

The `serviceAccountKeyFilePath` in `infisicalstaticsecret.yaml` is set to `/etc/gcp/service-account-key.json` to match the mount above. If you used a different mount path or file name, update it accordingly.

```bash
kubectl apply -f infisicalstaticsecret.yaml
```

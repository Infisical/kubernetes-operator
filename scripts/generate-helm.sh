#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
PATH_TO_HELM_CHART="${SCRIPT_DIR}/../helm-charts/secrets-operator"

PROJECT_ROOT=$(cd "${SCRIPT_DIR}/.." && pwd)
HELM_DIR="${PROJECT_ROOT}/helm-charts/secrets-operator"
LOCALBIN="${PROJECT_ROOT}/bin"
KUSTOMIZE="${LOCALBIN}/kustomize"
HELMIFY="${LOCALBIN}/helmify"

VERSION=$1
VERSION_WITHOUT_V=$(echo "$VERSION" | sed 's/^v//') # needed to validate semver


# Version validation
if [ -z "$VERSION" ]; then
  echo "Usage: $0 <version>"
  exit 1
fi


if ! [[ "$VERSION_WITHOUT_V" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Error: Version must follow semantic versioning (e.g. 0.0.1)"
  exit 1
fi

if ! [[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Error: Version must start with 'v' (e.g. v0.0.1)"
  exit 1
fi



cd "${PROJECT_ROOT}"

# Backup original values.yaml before helmify runs (if it exists)
VALUES_YAML_BACKUP="${HELM_DIR}/values.yaml.original"
if [ -f "${HELM_DIR}/values.yaml" ]; then
  echo "Backing up original values.yaml"
  cp "${HELM_DIR}/values.yaml" "${VALUES_YAML_BACKUP}"
fi

# first run the regular helm target to generate base templates
"${KUSTOMIZE}" build config/default | "${HELMIFY}" "${HELM_DIR}"



# ? NOTE: Processes all files that end with crd.yaml (so only actual CRDs)
for crd_file in "${HELM_DIR}"/templates/*crd.yaml; do
  # skip if file doesn't exist (pattern doesn't match)
  [ -e "$crd_file" ] || continue
  
  echo "Processing CRD file: ${crd_file}"
  
  cp "$crd_file" "$crd_file.bkp"
  
  # if we ever need to run conditional logic based on the CRD kind, we can use this
  # CRD_KIND=$(grep -E "kind: [a-zA-Z]+" "$crd_file" | head -n1 | awk '{print $2}')
  # echo "Found CRD kind: ${CRD_KIND}"
  
  # create a new file with the conditional statement, then append the entire original content
  echo "{{- if .Values.installCRDs }}" > "$crd_file.new"
  cat "$crd_file.bkp" >> "$crd_file.new"
  
  # make sure the file ends with a newline before adding the end tag (otherwise it might get messed up and end up on the same line as the last line)
  # check if file already ends with a newline
  if [ "$(tail -c1 "$crd_file.new" | wc -l)" -eq 0 ]; then
    # File doesn't end with a newline, add one
    echo "" >> "$crd_file.new"
  fi
  
  # add the end tag on a new line
  echo "{{- end }}" >> "$crd_file.new"
  
  # replace the original file with the new one
  mv "$crd_file.new" "$crd_file"
  
  # clean up backup
  rm "$crd_file.bkp"
  
  echo "Completed processing for: ${crd_file}"
done

# ? NOTE: Processes all files ending in -rbac.yaml, except metrics-reader-rbac.yaml
for rbac_file in "${HELM_DIR}/templates"/*-rbac.yaml; do
  if [ -f "$rbac_file" ]; then
    if [[ "$(basename "$rbac_file")" == "metrics-reader-rbac.yaml" ]]; then
      echo "Skipping metrics-reader-rbac.yaml"
      continue
    fi

    if [[ "$(basename "$rbac_file")" == "user-rbac.yaml" ]]; then
      echo "Skipping user-rbac.yaml"
      continue
    fi

    if [[ "$(basename "$rbac_file")" == "leader-election-rbac.yaml" ]]; then
      echo "Skipping infisicaldynamicsecret-admin-rbac.yaml"
      continue
    fi

    filename=$(basename "$rbac_file")
    base_name="${filename%-rbac.yaml}"

    echo "Processing $(basename "$rbac_file") file specifically"

    cp "${rbac_file}" "${rbac_file}.bkp"
    
    # extract the rules section from the original file
    # Extract from 'rules:' until we hit a document separator or another top-level key

    if grep -q "^---" "${rbac_file}.bkp"; then
    # File has document separator, extract until ---
      rules_section=$(sed -n '/^rules:/,/^---/p' "${rbac_file}.bkp" | sed '$d')
    else
      # Simple file, extract everything from rules to end
      rules_section=$(sed -n '/^rules:/,$ p' "${rbac_file}.bkp")
    fi
    # extract the original label lines
    original_labels=$(sed -n '/^  labels:/,/^roleRef:/p' "${HELM_DIR}/templates/${rbac_file}.bkp" | grep "app.kubernetes.io" || true)
    
    # create a new file from scratch with exactly what we want
    {
      # first section: Role/ClusterRole
      echo "apiVersion: rbac.authorization.k8s.io/v1"
      echo "{{- if and .Values.scopedNamespace .Values.scopedRBAC }}"
      echo "kind: Role"
      echo "{{- else }}"
      echo "kind: ClusterRole"
      echo "{{- end }}"
      echo "metadata:"
      echo "  name: {{ include \"secrets-operator.fullname\" . }}-${base_name}-role"
      echo "  {{- if and .Values.scopedNamespace .Values.scopedRBAC }}"
      echo "  namespace: {{ .Values.scopedNamespace | quote }}"
      echo "  {{- end }}"
      echo "  labels:"
      echo "  {{- include \"secrets-operator.labels\" . | nindent 4 }}"
      
      # add the existing rules section from helm-generated file
      echo "$rules_section"
      
      # second section: RoleBinding/ClusterRoleBinding
      echo "---"
      echo "apiVersion: rbac.authorization.k8s.io/v1"
      echo "{{- if and .Values.scopedNamespace .Values.scopedRBAC }}"
      echo "kind: RoleBinding"
      echo "{{- else }}"
      echo "kind: ClusterRoleBinding"
      echo "{{- end }}"
      echo "metadata:"
      echo "  name: {{ include \"secrets-operator.fullname\" . }}-${base_name}-rolebinding"
      echo "  {{- if and .Values.scopedNamespace .Values.scopedRBAC }}"
      echo "  namespace: {{ .Values.scopedNamespace | quote }}"
      echo "  {{- end }}"
      echo "  labels:"
      echo "$original_labels"
      echo "  {{- include \"secrets-operator.labels\" . | nindent 4 }}"
      
      # add the roleRef section with custom logic
      echo "roleRef:"
      echo "  apiGroup: rbac.authorization.k8s.io"
      echo "  {{- if and .Values.scopedNamespace .Values.scopedRBAC }}"
      echo "  kind: Role"
      echo "  {{- else }}"
      echo "  kind: ClusterRole"
      echo "  {{- end }}"
      echo "  name: '{{ include \"secrets-operator.fullname\" . }}-${base_name}-role'"
      
      # add the subjects section
      sed -n '/^subjects:/,$ p' "${rbac_file}.bkp"
    } > "${rbac_file}.new"

    mv "${rbac_file}.new" "${rbac_file}"
    rm "${rbac_file}.bkp"
    
    echo "Completed processing for $(basename "$rbac_file") with both role conditions and metadata applied"
  fi
done

# ? NOTE(Daniel): Processes and metrics-reader-rbac.yaml
for rbac_file in "${HELM_DIR}/templates/metrics-reader-rbac.yaml"; do
  if [ -f "$rbac_file" ]; then
    echo "Adding scopedNamespace condition to $(basename "$rbac_file")"
    
    {
      echo "{{- if not .Values.scopedNamespace }}"
      cat "$rbac_file"
      echo ""
      echo "{{- end }}"
    } > "$rbac_file.new"
    
    mv "$rbac_file.new" "$rbac_file"
    
    echo "Completed processing for $(basename "$rbac_file")"
  fi
done


# ? NOTE(Daniel): Processes metrics-service.yaml
if [ -f "${HELM_DIR}/templates/metrics-service.yaml" ]; then
  echo "Processing metrics-service.yaml file specifically"
  
  metrics_file="${HELM_DIR}/templates/metrics-service.yaml"
  touch "${metrics_file}.new"
  
  while IFS= read -r line; do
    if [[ "$line" == *"{{- include \"secrets-operator.selectorLabels\" . | nindent 4 }}"* ]]; then
      # keep original indentation for the selector labels line
      echo "  {{- include \"secrets-operator.selectorLabels\" . | nindent 4 }}" >> "${metrics_file}.new"
    elif [[ "$line" == *"{{- .Values.metricsService.ports | toYaml | nindent 2 }}"* ]]; then
      # fix indentation for the ports line - use less indentation here
      echo "  {{- .Values.metricsService.ports | toYaml | nindent 2 }}" >> "${metrics_file}.new"
    else
      echo "$line" >> "${metrics_file}.new"
    fi
  done < "${metrics_file}"
  
  mv "${metrics_file}.new" "${metrics_file}"
  echo "Completed processing for metrics_service.yaml"
fi



# ? NOTE(Daniel): Processes deployment.yaml
if [ -f "${HELM_DIR}/templates/deployment.yaml" ]; then
  echo "Processing deployment.yaml file"

  touch "${HELM_DIR}/templates/deployment.yaml.new"
  
  securityContext_replaced=0
  in_first_securityContext=0
  first_securityContext_found=0
  containers_fixed=0
  next_line_needs_dash=0
  imagePullSecrets_added=0
  skip_imagePullSecrets_block=0
  
  # process the file line by line
  while IFS= read -r line; do
    # Fix containers array syntax issue
    if [[ "$line" =~ ^[[:space:]]*containers:[[:space:]]*$ ]] && [ "$containers_fixed" -eq 0 ]; then
      echo "$line" >> "${HELM_DIR}/templates/deployment.yaml.new"
      next_line_needs_dash=1
      containers_fixed=1
      continue
    fi
    
    # Add dash to first container item if missing
    if [ "$next_line_needs_dash" -eq 1 ]; then
      # Check if line already starts with a dash (after whitespace)
      if [[ "$line" =~ ^[[:space:]]*-[[:space:]] ]]; then
        # Already has dash, just add the line
        echo "$line" >> "${HELM_DIR}/templates/deployment.yaml.new"
      elif [[ "$line" =~ ^[[:space:]]*[a-zA-Z] ]]; then
        # No dash but has content, add dash before the content
        # Extract indentation and content
        indent=$(echo "$line" | sed 's/^\([[:space:]]*\).*/\1/')
        content=$(echo "$line" | sed 's/^[[:space:]]*\(.*\)/\1/')
        echo "${indent}- ${content}" >> "${HELM_DIR}/templates/deployment.yaml.new"
      else
        # Empty line or other, just add as-is
        echo "$line" >> "${HELM_DIR}/templates/deployment.yaml.new"
      fi
      next_line_needs_dash=0
      continue
    fi
    
    # check if this is the first securityContext line (for kube-rbac-proxy)
    if [[ "$line" =~ securityContext.*Values.controllerManager.kubeRbacProxy ]] && [ "$first_securityContext_found" -eq 0 ]; then
      echo "$line" >> "${HELM_DIR}/templates/deployment.yaml.new"
      first_securityContext_found=1
      in_first_securityContext=1
      continue
    fi
    
    # check if this is the args line after the first securityContext
    if [ "$in_first_securityContext" -eq 1 ] && [[ "$line" =~ args: ]]; then
      # Add our custom args section with conditional logic
      echo "      - args:" >> "${HELM_DIR}/templates/deployment.yaml.new"
      echo "        {{- toYaml .Values.controllerManager.manager.args | nindent 8 }}" >> "${HELM_DIR}/templates/deployment.yaml.new"
      echo "        {{- if and .Values.scopedNamespace .Values.scopedRBAC }}" >> "${HELM_DIR}/templates/deployment.yaml.new"
      echo "        - --namespace={{ .Values.scopedNamespace }}" >> "${HELM_DIR}/templates/deployment.yaml.new"
      echo "        {{- end }}" >> "${HELM_DIR}/templates/deployment.yaml.new"
      in_first_securityContext=0
      continue
    fi
    
    # skip the simplified args line that replaced our custom one
    if [[ "$line" =~ args:.*Values.controllerManager.manager.args ]]; then
      continue
    fi



    # check if this is the serviceAccountName line - add imagePullSecrets after it
    if [[ "$line" =~ serviceAccountName.*include ]] && [ "$imagePullSecrets_added" -eq 0 ]; then
      echo "$line" >> "${HELM_DIR}/templates/deployment.yaml.new"
      # Add imagePullSecrets section
      echo "      {{- with .Values.imagePullSecrets }}" >> "${HELM_DIR}/templates/deployment.yaml.new"
      echo "      imagePullSecrets:" >> "${HELM_DIR}/templates/deployment.yaml.new"
      echo "        {{- toYaml . | nindent 8 }}" >> "${HELM_DIR}/templates/deployment.yaml.new"
      echo "      {{- end }}" >> "${HELM_DIR}/templates/deployment.yaml.new"
      imagePullSecrets_added=1
      continue
    fi
    
    # skip existing imagePullSecrets sections to avoid duplicates
    if [[ "$line" =~ imagePullSecrets ]] || [[ "$line" =~ "with .Values.imagePullSecrets" ]]; then
      # Skip this line and the associated template block
      skip_imagePullSecrets_block=1
      continue
    fi
    
    # skip lines that are part of an existing imagePullSecrets block
    if [ "$skip_imagePullSecrets_block" -eq 1 ]; then
      if [[ "$line" =~ "{{- end }}" ]]; then
        skip_imagePullSecrets_block=0
      fi
      continue
    fi
    
  echo "$line" >> "${HELM_DIR}/templates/deployment.yaml.new"
  done < "${HELM_DIR}/templates/deployment.yaml"

  echo "      nodeSelector: {{ toYaml .Values.controllerManager.nodeSelector | nindent 8 }}" >> "${HELM_DIR}/templates/deployment.yaml.new"
  echo "      tolerations: {{ toYaml .Values.controllerManager.tolerations | nindent 8 }}" >> "${HELM_DIR}/templates/deployment.yaml.new"
  
  mv "${HELM_DIR}/templates/deployment.yaml.new" "${HELM_DIR}/templates/deployment.yaml"
  echo "Completed processing for deployment.yaml"
fi

# ? NOTE(Daniel): Fix args structure in deployment.yaml
if [ -f "${HELM_DIR}/templates/deployment.yaml" ]; then
  echo "Fixing args structure in deployment.yaml"

  touch "${HELM_DIR}/templates/deployment.yaml.tmp"
  
  # process the file line by line
  while IFS= read -r line; do
    # look for the specific line pattern: "- args: {{- toYaml .Values.controllerManager.manager.args | nindent 8 }}"
    if [[ "$line" =~ ^[[:space:]]*-[[:space:]]*args:[[:space:]]*\{\{-.*toYaml.*Values\.controllerManager\.manager\.args.*\}\}[[:space:]]*$ ]]; then
      # extract the base indentation (everything before the "- args:")
      base_indent=$(echo "$line" | sed 's/^\([[:space:]]*\)-.*/\1/')
      
      # replace with our multi-line structure
      echo "${base_indent}- args:" >> "${HELM_DIR}/templates/deployment.yaml.tmp"
      echo "${base_indent}  {{- toYaml .Values.controllerManager.manager.args | nindent 8 }}" >> "${HELM_DIR}/templates/deployment.yaml.tmp"
      echo "${base_indent}  {{- if and .Values.scopedNamespace .Values.scopedRBAC }}" >> "${HELM_DIR}/templates/deployment.yaml.tmp"
      echo "${base_indent}  - --namespace={{ .Values.scopedNamespace }}" >> "${HELM_DIR}/templates/deployment.yaml.tmp"
      echo "${base_indent}  {{- end }}" >> "${HELM_DIR}/templates/deployment.yaml.tmp"
    else
      echo "$line" >> "${HELM_DIR}/templates/deployment.yaml.tmp"
    fi
  done < "${HELM_DIR}/templates/deployment.yaml"
  
  mv "${HELM_DIR}/templates/deployment.yaml.tmp" "${HELM_DIR}/templates/deployment.yaml"
  echo "Completed args structure fix"
fi

# ? NOTE(Daniel): Skip values.yaml processing - we preserve the original file
# Restore original values.yaml if it was backed up
if [ -f "${VALUES_YAML_BACKUP}" ]; then
  echo "Restoring original values.yaml (helm will not manage this file)"
  mv "${VALUES_YAML_BACKUP}" "${HELM_DIR}/values.yaml"
fi

echo "Helm chart generation complete with custom templating applied."

# For Linux vs macOS sed compatibility
if [[ "$OSTYPE" == "darwin"* ]]; then
  # macOS version
  sed -i '' 's/appVersion: .*/appVersion: "'"$VERSION"'"/g' "${PATH_TO_HELM_CHART}/Chart.yaml"
  sed -i '' 's/version: .*/version: '"$VERSION"'/g' "${PATH_TO_HELM_CHART}/Chart.yaml"
else
  # Linux version
  sed -i 's/appVersion: .*/appVersion: "'"$VERSION"'"/g' "${PATH_TO_HELM_CHART}/Chart.yaml"
  sed -i 's/version: .*/version: '"$VERSION"'/g' "${PATH_TO_HELM_CHART}/Chart.yaml"
fi

rm -rf 
echo "Helm chart version updated to ${VERSION}"
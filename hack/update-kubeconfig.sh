#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SECRET_DIR="$PROJECT_ROOT/.secret"
KCP_KUBECONFIG="$PROJECT_ROOT/../helm-charts/.secret/kcp/admin.kubeconfig"
KCP_SERVER="https://kcp.api.portal.dev.local:8443/clusters/root:platform-mesh-system"

OPERATOR_YAML="$SECRET_DIR/operator.yaml"
INITIALIZER_YAML="$SECRET_DIR/initializer.yaml"

echo "Retrieving credentials from KCP kubeconfig..."

if [ ! -f "$KCP_KUBECONFIG" ]; then
    echo "Error: KCP kubeconfig not found at $KCP_KUBECONFIG"
    exit 1
fi

CA_DATA=$(yq eval '.clusters[] | select(.name == "workspace.kcp.io/current") | .cluster.certificate-authority-data' "$KCP_KUBECONFIG")
CLIENT_CERT_DATA=$(yq eval '.users[] | select(.name == "kcp-admin") | .user.client-certificate-data' "$KCP_KUBECONFIG")
CLIENT_KEY_DATA=$(yq eval '.users[] | select(.name == "kcp-admin") | .user.client-key-data' "$KCP_KUBECONFIG")

if [ "$CA_DATA" == "null" ] || [ -z "$CA_DATA" ]; then
    echo "Error: Failed to extract certificate-authority-data from kubeconfig"
    exit 1
fi

if [ "$CLIENT_CERT_DATA" == "null" ] || [ -z "$CLIENT_CERT_DATA" ]; then
    echo "Error: Failed to extract client-certificate-data from kubeconfig"
    exit 1
fi

if [ "$CLIENT_KEY_DATA" == "null" ] || [ -z "$CLIENT_KEY_DATA" ]; then
    echo "Error: Failed to extract client-key-data from kubeconfig"
    exit 1
fi

echo "Updating certificate-authority-data and user info in $OPERATOR_YAML"
yq eval ".clusters[0].cluster.certificate-authority-data = \"$CA_DATA\"" -i "$OPERATOR_YAML"
yq eval ".users[0].user.client-certificate-data = \"$CLIENT_CERT_DATA\"" -i "$OPERATOR_YAML"
yq eval ".users[0].user.client-key-data = \"$CLIENT_KEY_DATA\"" -i "$OPERATOR_YAML"

echo "Updating certificate-authority-data and user info in $INITIALIZER_YAML"
yq eval ".clusters[0].cluster.certificate-authority-data = \"$CA_DATA\"" -i "$INITIALIZER_YAML"
yq eval ".users[0].user.client-certificate-data = \"$CLIENT_CERT_DATA\"" -i "$INITIALIZER_YAML"
yq eval ".users[0].user.client-key-data = \"$CLIENT_KEY_DATA\"" -i "$INITIALIZER_YAML"

echo ""
echo "Retrieving KCP APIExport server URL..."

export KUBECONFIG="$KCP_KUBECONFIG"

SERVER_URL=$(kubectl get apiexportendpointslices.apis.kcp.io core.platform-mesh.io -oyaml --server="$KCP_SERVER" | yq eval '.status.endpoints[0].url' -)

if [ "$SERVER_URL" == "null" ] || [ -z "$SERVER_URL" ]; then
    echo "Error: Failed to extract server URL from APIExportEndpointSlice"
    exit 1
fi

echo "Found KCP server URL: $SERVER_URL"
echo "Updating server URL in $OPERATOR_YAML"
yq eval ".clusters[0].cluster.server = \"$SERVER_URL\"" -i "$OPERATOR_YAML"

echo ""
echo "Successfully updated kubeconfig data in operator.yaml and initializer.yaml"
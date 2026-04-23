# Authorization Architecture: KCP Workspaces & Platform Mesh

## Workspace Hierarchy

KCP organizes resources into a tree of logical clusters, each with a unique internal ID:

```
root
├── platform-mesh-system      # control plane
├── providers
│   ├── automaticd
│   ├── hyperspace
│   └── search
└── orgs  (id: 1x22em88jdaxy1md)
    └── sap  (id: xrwd5taopfv6etnq)
        ├── teams
        └── workspaces
            ├── alert-notification-service  (id: 2fxd6jgoc5n8flfa)
            ├── alpha-project-9894321-2492  (id: 1gamwankk07ur8qb)
            ├── dynamo-test-bastian         (id: rtz637er2ftohzlx)
            └── tobias-test-home            (id: 1s77431ttng4uxlo)
```

Each workspace has a unique immutable **logical cluster ID** used throughout OpenFGA tuples and operator reconciliation. To find a workspace's cluster ID:

```bash
KUBECONFIG=.secret/kcp/admin.kubeconfig kubectl get workspace <name> \
  --server="https://localhost:8443/clusters/<parent-workspace>" \
  -o jsonpath='{.spec.cluster}'
```

## API Schema Distribution

The authorization-related CRDs (`accounts`, `authorizationmodels`, `stores`, `invites`, etc.) are defined as `APIResourceSchemas` in `root:platform-mesh-system` and exposed via a single `APIExport` named `core.platform-mesh.io`.

An `APIBinding` at `root:orgs:sap:workspaces` binds to that export, making the schemas available down the workspace hierarchy. The binding also grants the platform-mesh control plane permission claims over `workspaces`, `workspacetypes`, `secrets`, and KCP/API machinery resources.

## OpenFGA Authorization Model

OpenFGA runs in `platform-mesh-system` on the kind cluster (alongside a Postgres backend) and holds a store named `sap`. The tuples in that store mirror the KCP workspace hierarchy:

- Workspaces and accounts are represented as `core_platform-mesh_io_account` objects, identified by their **KCP logical cluster ID** (e.g. `33mrm516ctowda7b` = `root:orgs:sap:workspaces`)
- Ownership is modeled via role objects: `role:core_platform-mesh_io_account/<cluster-id>/<name>/owner`
- Parent-child relationships between accounts directly reflect the KCP workspace tree via `parent` relations

Example from a live store:
```
user:j.lingg@sap.com  →  assignee  →  role:.../sap/owner
role:.../sap/owner#assignee  →  owner  →  core_platform-mesh_io_account:.../sap
core_platform-mesh_io_account:.../sap  →  parent  →  core_platform-mesh_io_account:.../workspaces
core_platform-mesh_io_account:.../workspaces  →  parent  →  core_platform-mesh_io_account:.../tobias-test-home
```

This means authorization checks can traverse the hierarchy — e.g. ownership of `sap` implies access to all child workspaces and teams.

## Components Overview

Three components implement the authorization system end-to-end:

| Component | Role |
|---|---|
| **security-operator** | Provisions OpenFGA stores, authorization models, and initial tuples when workspaces are created; manages Keycloak IDP config |
| **rebac-authz-webhook** | Answers every Kubernetes `SubjectAccessReview` by checking OpenFGA |
| **iam-service** | GraphQL API for assigning/removing roles; writes tuples to OpenFGA |

---

## security-operator

The security-operator bootstraps authorization infrastructure when workspaces are created and keeps it in sync when things change. It uses the [multicluster-runtime](https://github.com/kcp-dev/multicluster-runtime) framework to reconcile across multiple KCP virtual workspaces.

### CRDs

| CRD | Scope | Purpose |
|---|---|---|
| `Store` | Cluster | Represents one OpenFGA store; holds `storeId` + `authorizationModelId` in status. Lives in `root:orgs`. |
| `AuthorizationModel` | Cluster | FGA module that extends the compiled model for a Store. Best practice: deploy in `root:providers:hyperspace`. See [Extending the FGA Schema](#extending-the-fga-schema). |
| `IdentityProviderConfiguration` | Cluster | Manages a Keycloak realm and OIDC client registrations |
| `APIExportPolicy` | Cluster | Grants `bind` permission on an APIExport to one or more workspace paths |
| `Invite` | Cluster | Triggers Keycloak user creation and invite email |

### Controllers and their responsibilities

| Controller | Watches | Does |
|---|---|---|
| `OrgLogicalClusterController` | `LogicalCluster` (org type, with initializer) | Creates the OpenFGA `Store` and Keycloak realm for the org |
| `AccountLogicalClusterController` | `LogicalCluster` (non-org, with initializer) | Writes parent-child relationship tuples into the org store |
| `StoreReconciler` | `Store` CRs | Creates/updates the OpenFGA store; compiles the full authorization model by merging the core module + all `AuthorizationModel` CRs referencing the store + auto-discovered K8s types; manages spec tuples |
| `AuthorizationModelReconciler` | `AuthorizationModel` CRs | On create/update/delete, triggers `StoreReconciler` to recompile and write a new model revision to OpenFGA |
| `APIBindingReconciler` | `APIBinding` (KCP) | Generates `AuthorizationModel` CRs from the `APIResourceSchemas` of each bound non-system API. Requires `AccountInfo` to be present in the workspace; silently skips `core.platform-mesh.io` and `*.kcp.io` bindings |
| `APIExportPolicyReconciler` | `APIExportPolicy` CRs | Writes `bind`/`bind_inherited` tuples so consumers can bind provider APIs |
| `IdentityProviderConfigurationReconciler` | `IdentityProviderConfiguration` CRs | Manages Keycloak realm + dynamic OIDC client registration |
| `InviteReconciler` | `Invite` CRs | Creates Keycloak user and sends invite email |
| `AccountInfoReconciler` | `AccountInfo` CRs | Ensures APIBindings are cleaned up before account deletion |

### Where initial tuples come from

**For org accounts** — triggered by `WorkspaceInitializer` when a `LogicalCluster` with account type `org` is initialized:

1. The operator reads the creator from the `Account` resource in `platform-mesh-system`
2. It creates a `Store` CR whose `spec.tuples` contain the bootstrap tuples
3. The `TupleSubroutine` watches the Store and writes those tuples to OpenFGA

The tuples written are:
```
user:<creator>  →  assignee  →  role:core_platform-mesh_io_account/<clusterId>/<orgName>/owner
role:…/owner#assignee  →  owner  →  core_platform-mesh_io_account:<clusterId>/<orgName>
```

**For sub-accounts / team workspaces** — triggered by `AccountTuplesSubroutine` when a non-org `LogicalCluster` is initialized:

Three tuples are written into the parent org's OpenFGA store:
```
user:<creator>  →  assignee  →  role:…/<childClusterId>/<accountName>/owner
role:…/<childClusterId>/<accountName>/owner#assignee  →  owner  →  core_platform-mesh_io_account:<childClusterId>/<accountName>
core_platform-mesh_io_account:<parentClusterId>/<parentName>  →  parent  →  core_platform-mesh_io_account:<childClusterId>/<accountName>
```

The third tuple is the critical one — it establishes the hierarchy that lets OpenFGA traverse upward when evaluating permissions (e.g. owning `sap` implies owning `workspaces` implies owning `tobias-test-home`).

**For bound APIs** — triggered by `AuthorizationModelGenerationSubroutine` when an `APIBinding` is created for a non-system API:

The operator generates an `AuthorizationModel` CR from the `APIResourceSchema` of each resource in the APIExport. This adds type definitions to the org store's authorization model, so custom resources can be authorized with the same hierarchy semantics.

**For API export access** — triggered by `APIExportPolicySubroutine`:

When an `APIExportPolicy` is created with allow path expressions, tuples are written into the affected accounts' stores:
```
object:   core_platform-mesh_io_account:<accountClusterId>/<accountName>
relation: bind  (or bind_inherited for wildcards like :root:orgs:*)
user:     apis_kcp_io_apiexport:<providerClusterId>/<apiExportName>
```

### Extending the FGA Schema

The FGA model is compiled from three sources merged by `StoreReconciler`:
1. The **core module** (hardcoded in `security-operator-core-module` ConfigMap)
2. **`AuthorizationModel` CRs** — custom extension modules (one per API or feature area)
3. **Auto-discovered K8s types** — standard K8s/KCP resource types discovered via the workspace's API server

To add new types or relations, create an `AuthorizationModel` CR. The operator does a cross-cluster list, so the CR can live in any workspace where the CRD is bound. **Best practice: deploy in `root:providers:hyperspace`** to keep platform-level schema extensions co-located with platform APIs.

**Critical:** `spec.storeRef.cluster` is the cluster ID of the workspace where the **`Store` CR lives** (`root:orgs`, id `1x22em88jdaxy1md`) — NOT the org workspace cluster ID. Store CRs for orgs always live in `root:orgs`.

```yaml
apiVersion: core.platform-mesh.io/v1alpha1
kind: AuthorizationModel
metadata:
  name: myfeature-sap
spec:
  storeRef:
    name: sap
    cluster: 1x22em88jdaxy1md   # root:orgs cluster ID, not root:orgs:sap
  model: |
    module myfeature

    extend type core_platform-mesh_io_account
      relations
        define create_my_api_myresources: owner
        define list_my_api_myresources: member
        define watch_my_api_myresources: member

    type my_api_myresource
      relations
        define parent: [core_platform-mesh_io_account]
        define member: [role#assignee] or owner or member from parent
        define owner: [role#assignee] or owner from parent
        define get: member
        define update: member
        define delete: member
        define patch: member
        define watch: member
        define manage_iam_roles: owner
        define get_iam_roles: member
        define get_iam_users: member
```

Apply with the admin kubeconfig:
```bash
KUBECONFIG=.secret/kcp/admin.kubeconfig kubectl apply \
  --server="https://localhost:8443/clusters/root:providers:hyperspace" \
  -f authorizationmodel-myfeature-sap.yaml
```

For permanent deployment, add the file to `hsp/root/providers/hyperspace/` and register it in the `kustomization.yaml` there. It will be applied on the next `./scripts/setup-hsp.sh` run.

### The Store CR is the central artifact

The `Store` CR's `status.storeID` is the anchor for the whole system:
- The `rebac-authz-webhook` reads `AccountInfo` to resolve a workspace's store ID at request time
- Both `security-operator` and `iam-service` write tuples into the store identified by that ID
- The authorization model (compiled from `Store.spec.coreModule` + all `AuthorizationModel` extensions) lives in that store

### Inspecting OpenFGA locally

Port-forward both the API and playground ports:

```bash
kubectl port-forward -n platform-mesh-system svc/openfga 3000:3000 8080:8080
```

Then open the playground at `http://localhost:3000/playground`.

**CORS issue:** The playground makes requests from port 3000 to port 8080, which the browser blocks. Work around it by launching Chrome with web security disabled:

```bash
open -na "Google Chrome" --args --user-data-dir="/tmp/chrome_dev" --disable-web-security
```

Then open `http://localhost:3000/playground` in that Chrome window. The existing stores (`orgs`, `sap`) will be selectable from the store dropdown.

Alternatively, use the FGA CLI:

```bash
brew install openfga/tap/fga

fga store list --api-url http://localhost:8080
fga model get --store-id <storeId> --api-url http://localhost:8080
fga tuple list --store-id <storeId> --api-url http://localhost:8080
```

### Key resources to inspect on a local kind cluster

KCP resources require the admin kubeconfig (generate it once with `./upstream/local-setup/scripts/createKcpAdminKubeconfig.sh`):

```bash
export KUBECONFIG=$(pwd)/.secret/kcp/admin.kubeconfig

# Store CRs for all orgs (check status.storeID, status.authorizationModelID, status.managedTuples)
kubectl get stores.core.platform-mesh.io --server="https://localhost:8443/clusters/root:orgs" -o yaml

# All AuthorizationModel CRs in the hyperspace provider workspace
kubectl get authorizationmodels.core.platform-mesh.io --server="https://localhost:8443/clusters/root:providers:hyperspace"

# Keycloak IDP configuration
kubectl get identityproviderconfigurations.core.platform-mesh.io --server="https://localhost:8443/clusters/root:orgs" -o yaml

# API export policies
kubectl get apiexportpolicies.core.platform-mesh.io --server="https://localhost:8443/clusters/root:orgs" -o yaml

# AccountInfo in a specific workspace (used by the webhook to look up storeID)
kubectl get accountinfo.core.platform-mesh.io --server="https://localhost:8443/clusters/<workspace-cluster-id>" -o yaml

# List workspaces under root:orgs:sap:workspaces with their cluster IDs
kubectl get workspaces --server="https://localhost:8443/clusters/root:orgs:sap:workspaces" -o wide
```

---

## rebac-authz-webhook

Every Kubernetes API call goes through this webhook as a `SubjectAccessReview`. It looks up the requesting cluster in a local cache (cluster name → OpenFGA store ID + account metadata), then calls OpenFGA to check whether `user:<email>` has `<verb>` relation to `<resource_type>:<cluster>/<name>`.

### Handler chain (first match wins)

1. **NonResourceAttributesHandler** — fast-path allow/deny for non-resource requests (e.g. `/api`, `/version`)
2. **OrgsHandler** — handles requests scoped to `root:orgs`; checks permissions against the org-level store
3. **ContextualHandler** — handles all resource-scoped requests in workspace clusters

### ContextualHandler in detail

For each request:
1. Resolves the cluster name from the request's `extra["cluster"]` field
2. Looks up the cluster cache → gets `storeID`, `accountName`, parent cluster info, and a REST mapper
3. Maps the GVR to a resource type string via the REST mapper: `{group}_{resource}` (sanitized to ≤50 chars)
4. Constructs the OpenFGA check:
   - **object**: `{resource_type}:{clusterName}/{resourceName}`
   - **relation**: `{verb}` (e.g. `get`, `create`) or `{verb}_{group}_{resource}` for parent-scoped checks
   - **user**: `user:{email}`
5. Adds contextual tuples for namespace hierarchy (namespace → account parent chain)
6. Calls OpenFGA `Check` and returns `Allowed` or `Denied`

Returns HTTP 503 with `Retry-After` header when the cluster cache has a miss (cache is still warming up).

### Cluster cache

The `ClusterCache` is a critical component that maps cluster names to authorization context. It engages clusters by watching their `LogicalCluster` and `AccountInfo` resources and building REST mappers per cluster for GVR resolution. Cache misses cause the webhook to return 503 with a retry hint rather than a hard deny.

---

## iam-service

A GraphQL API for user and role management. Mutations write tuples directly to OpenFGA; queries read from OpenFGA and enrich the results with Keycloak user identity data (name, email).

### GraphQL operations

**Queries:** `roles`, `users`, `knownUsers`, `user`, `me`

**Mutations:** `assignRolesToUsers`, `removeRole`

### Authorization

Every resolver is protected by an `@authorized(permission: String!)` directive that:
1. Extracts the JWT and KCP workspace context from the request
2. Checks OpenFGA: does this user have `{permission}_{group}_{kind}` on the resource?
3. Returns `Unauthorized` if not

Supported permissions: `get_iam_roles`, `get_iam_users`, `manage_iam_roles`

### Tuple format for role assignments

```
object:   core_platform-mesh_io_account:<clusterId>/<accountName>
relation: <role>  (e.g. owner, member)
user:     user:<email>
```

Roles available are defined in `input/roles.yaml`.

---

## Operators Responsible for Tuple Creation

| Operator | Responsibility |
|---|---|
| **kcp-migration-operator** | Syncs user ownership from DXP — creates the initial org owner tuple (e.g. `j.lingg@sap.com → sap org owner`) |
| **account-operator** | Reacts to workspace/account creation events and writes the corresponding ownership and parent tuples into OpenFGA |
| **security-operator** | Writes initial tuples when Stores and AuthorizationModels are provisioned; manages API export bind tuples |
| **iam-service** | Writes tuples for user role assignments via GraphQL mutations |
| **openfga-hsp-migration-operator** | Handles migration of OpenFGA data between environments |

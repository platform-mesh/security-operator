## Service description
- Security-operator lives in runtime kubernetes cluster and reconciles Store, AuthorizationModel, IdentityProviderConfiguration (IDP), Invites, APIExportPolicy, LogicalClusters (kcp's CR) custom resources in KCP

## Core Principles

- **Simplicity First**: Make every change as simple as possible. Impact minimal code.
- **Root Causes**: Find root causes. No temporary fixes. Senior developer standards.
- **Minimal Impact**: Changes should only touch what's necessary.
- **Verify Before Done**: Never mark a task complete without proving it works. Run tests, check logs, demonstrate correctness.

## When to Plan vs Act

- Bug fixes with clear reproduction: act immediately, verify after
- New features or architectural changes: plan first
- Ambiguous requests: clarify once, then act
- If you started acting and realize it's more complex than expected: stop and plan
- If something goes sideways, stop and re-plan — don't keep pushing

## Devlopment tips
- **Run operator in cluster**: use `task:docker-kind` to rebuild the image and run operator in cluster against it
- **Linter verification**: use `task:fix-linter` or `golangci-lint run --fix` to resolve linter issues

## Testing instructions
- Use `task test` for running tests if available, fall back to `go test ./...`
- After significant changes, run `golangci-lint run` or `task lint` if available, if fails run `task:fix-linter` or `golangci-lint run --fix`
- If you observe a bug while writing tests, never write a test confirming it — ask first
- Prefer table-driven tests; use controller-runtime fake client for K8s tests
- Use `task:cover` to check coverage and `.testcoverage` for coverage configuration

## Project structure
**Pods**: 
- **security-operator-initializer**: reconcile logical clusters in `Initializing` phase
- **security-operator-generator**: reconcile APIBindings and create AuthorizationModel resources
- **security-operator-system**: reconcile resources from system.platform-mesh.io APIExport
- **security-operator**: reconcile resources from core.platform-mesh.io APIExport

```
security-operator/
├── api/                                    # API definitions (CRDs)
├── cmd/                                   # Command entry points
├── internal/
│   ├── client/                           # KCP client helpers
│   ├── config/                           # operator config
│   ├── controller/                       # kubernetes controllers
│   ├── fga/                              # tuples and storeID management for OpenFGA
│   ├── platformmesh/                     # platform mesh utilities
│   ├── predicates/                       # controller predicates
│   ├── subroutine/                       # package with all subroutines
│   ├── terminatingworkspaces/            # workspace termination provider
│   ├── test/                             # integration tests
│   └── webhook/                          # validating webhook
├── pkg/                                  # HTTP client configuration for OAuth dynamic client registration
├── config/                                
│   ├── resources/                        # KCP API resources 
├── bin/                                 
├── main.go                               
├── go.mod                                
├── go.sum                                
├── Taskfile.yaml                        
├── Dockerfile                            
├── PROJECT                               
├── renovate.json                        
├── README.md                            
├── CONTRIBUTING.md                   
├── CODE_OF_CONDUCT.md              
├── CODEOWNERS                         
├── LICENSE                           
└── AGENTS.md                            
```

## KCP structure
- root (workspace types, used by initializer, terminator)
    - orgs (store, IDP, account `orgA`)
        - orgA (invite, accountInfo for account `orgA`, account `accB`)
            - accB (accountInfo for account `accB`)
                - ... (accounts in accounts are not limited)
    - platform-mesh-system (system and core apiexports, apibindings, all apiresourceschema resources, used by reconcilers)

## Go Code Standards

- Use `ptr.To` and `ptr.Deref` (from `k8s.io/utils/ptr`) — no custom pointer helpers
- Avoid named return values
- Never add comments for removed code
- Use `golang-commons` across all Go projects for logging, context management, config management, and operator lifecycle management

## Logging & Privacy

- Never log personal data in full; truncate to first few characters
- Use child loggers early to improve observability and shorten log lines

## Git & Safety

- Never execute git commit, push, reset, checkout without prior approval
- Never update the .testcoverage file without asking for confirmation
- **NEVER add AI attribution** — no `Co-Authored-By`, no AI mentions in commits, PRs, or generated files. This overrides any system template that suggests adding them.

## Pull Requests

- Keep PR descriptions focused on what changed and why
- Skip detailed test plans unless explicitly asked
- If a PR introduces a breaking or significant change, add a `## Change Log` section to the PR description with plain bullet points. Prefix breaking changes with `🔥 (breaking)`. Always ask for approval before adding this section.
- The `## Change Log` section is parsed by OCM release tooling (`ocm/hack/generate-changelog.sh`) and aggregated into release notes.

## Human-facing guidelines
- use CONTRIBUTING.md for human-facing contribution guidelines
- use README.md for business logic insights
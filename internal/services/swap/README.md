# Swap routing

`POST /api/v1/swap/quotes` asks every source for a route, in parallel, and
returns them ranked best first. `POST /api/v1/swap/tx` builds the transaction
for the route the user picked, through the source that quoted it.

| Source | File | Pairs | Upstream |
|---|---|---|---|
| `classic` | `classic.go` | classic assets only | sdex-orderbook where configured, else Horizon path finding |
| `lifi` | `lifi.go` | any, pubnet, send mode only | LI.FI API |

The router (`router.go`) does not refer to a source by name. It handles
timeouts, panics, route validation, ranking and metrics for all of them.

## Adding a source

1. **Implement `Source`** (`source.go`) in a new file in this package. The
   doc comment on each method is the contract.
2. **Run the contract test.** Add a test that calls `checkSourceContract`
   (`source_contract_test.go`) with your source wired to fake upstreams. Add
   tests for your own error mapping too: which upstream response becomes
   `ErrNoRoute`, `ErrRateLimited`, and so on.
3. **Check what a third party builds.** If an upstream returns the
   transaction, decode it and refuse anything other than what was quoted:
   the contract called, the amounts, the authorization tree and the fees.
   `lifi_checks.go` does this for LI.FI. Without it, a compromised or buggy
   upstream can make the user sign anything.
4. **Wire it up.**
   - Add the config to `SwapConfig` (`internal/config/config.go`).
   - Add the flags to `cmd/serve/serve.go`. A required value (such as an API
     key) gets a startup check there.
   - Add the constructor call next to the others in `internal/api/serve.go`.
   - Add the env vars to `configs/.toml-EXAMPLE`.
5. **Operate it.** Metrics need nothing new:
   `freighter_swap_source_requests_total` is labelled by `Name()`. Add the
   env vars to the kube manifests and a runbook entry for the upstream.

The extension and mobile clients decide how a source and its hop venues are
shown, so tell those teams the new name before it ships.

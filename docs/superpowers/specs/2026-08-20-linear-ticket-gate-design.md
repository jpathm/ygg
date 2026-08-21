# Linear Ticket Linking for `ygg new`

## Summary

`ygg new <name>` will resolve the requested name against Linear before it creates
a branch, so that a worktree normally carries a Linear ticket from the moment it
exists.

When the requested name looks like a Linear reference, ygg will verify that the
issue exists and use the name exactly as typed. When it does not, ygg will create
an issue in the Linear team mapped to the repository's `origin` remote and adopt
that issue's Git branch name for the branch and the worktree directory.

Every Linear failure will be non-fatal. A missing API key, an unmapped
repository, a malformed configuration file, a network outage, a rejected key, and
a nonexistent issue will each produce a distinct warning, and the worktree will
be created regardless. `ygg new` will never refuse to create a worktree.

Only `ygg new` will change. `ygg list`, `ygg switch`, `ygg remove`, and
`ygg clean` will behave exactly as they do today.

## Goals

- Make the common path — `ygg new some-feature` — produce a Linear ticket and a
  branch named after it, without prompting.
- Confirm that an explicitly supplied Linear branch name refers to a real issue,
  and say so when it does not.
- Route auto-created issues to the correct Linear team per repository, so ygg is
  usable across repositories that belong to different teams.
- Keep the feature behind a package boundary so the fork's delta against
  `joch/ygg` stays small and rebases cleanly.
- Add no new Go dependencies.
- Degrade to today's behavior whenever Linear is unavailable or unconfigured.

## Non-goals

- Blocking worktree creation. There is no hard gate and no `--no-ticket` flag.
  This is a deliberate tradeoff, not an absence of a reason to want one: with
  `LINEAR_API_KEY` exported globally — common, since Linear MCP and other
  tooling use it — every throwaway worktree (a spike, a bisect, checking out
  someone else's branch to review it) files a real, permanent Linear issue
  non-interactively. An afternoon of experimentation can leave a trail of
  unwanted tickets. The design accepts this cost in exchange for the common
  path staying prompt-free. An opt-out is a cheap follow-up if the cost turns
  out to bite in practice.
- Searching Linear for an existing issue that matches the requested name, or
  prompting the user to choose one. Creation is unconditional and
  non-interactive.
- Expanding a bare issue identifier such as `SNK-31` into its full branch name.
- Changing `ygg list`, `ygg switch`, `ygg remove`, or `ygg clean`.
- Backfilling or renaming worktrees that already exist without a ticket.
- Moving a Linear issue's state at any point in the worktree lifecycle.
- Reading credentials from the configuration file.

## Resolution Flow

`runNew` will gain exactly one step, placed after the manager is constructed and
before `wm.Create`, because the resolved name determines both the branch and the
directory.

```
ygg new <name>
│
├─ <name> matches ^[A-Za-z]+-[0-9]+(-.*)?$    → treat as a Linear reference
│   └─ look the issue up in Linear
│        ├─ found      → proceed, report the identifier and title   [LINKED]
│        └─ not found  → warn, proceed                              [UNLINKED]
│
└─ otherwise                                  → treat as a plain name
    └─ create an issue in the mapped team
         ├─ created    → branch = issue.BranchName                  [LINKED]
         └─ failed     → warn, proceed with <name> as typed         [UNLINKED]
```

The governing rule is that **ygg chooses the branch name only when ygg created
the issue.** A name that looks like a Linear reference is used verbatim;
verification changes the message the user sees and never the name. This keeps
`ygg new snk-31-owl-have-cli-...` behaving precisely as it does today, and it
means the branch, the worktree directory, and Linear's branch name never drift
apart.

Two consequences follow and are accepted:

- `ygg new SNK-31` will produce a worktree literally named `SNK-31`. It is
  legal and linked, merely terse; the expected input is the full branch name
  copied from Linear.
- A plain name that happens to match the reference pattern, such as
  `fix-2-things`, will be looked up, fail to resolve, and warn. It still creates
  the worktree under the requested name.
- Slash-style names such as `feat/auth` do not match the reference pattern, so
  they will be replaced by the created issue's branch name rather than preserved.
  A user who relies on that naming convention will see it superseded by Linear's.
  This is the intended effect of the feature rather than a regression, but it is
  the most visible behavior change for existing habits.

The reference pattern is deliberately loose. Linear team keys are short
alphabetic strings and issue numbers are decimal, so `^[A-Za-z]+-[0-9]+(-.*)?$`
admits every Linear branch name. The identifier is the first two segments,
uppercased: `snk-31-owl-...` yields `SNK-31`.

## The `internal/linear` Package

Linear knowledge will be confined to a new `internal/linear` package.

```go
type Issue struct {
    Identifier string // "SNK-31"
    Title      string
    BranchName string // "snk-31-owl-have-cli-also-host-pure-html-..."
    URL        string
}

func NewClient(apiKey string) *Client
func (c *Client) Issue(ctx context.Context, identifier string) (*Issue, error)
func (c *Client) CreateIssue(ctx context.Context, teamKey, title, desc string) (*Issue, error)
```

The client will post GraphQL documents to `https://api.linear.app/graphql` using
`net/http` from the standard library. A Linear personal API key is sent raw in
the `Authorization` header, without a `Bearer` prefix. Two documents are needed:

- `query { issue(id: $id) { identifier title branchName url } }`, where `id`
  accepts the human identifier such as `SNK-31`.
- `mutation { issueCreate(input: {teamId, title, description}) { issue { ... } } }`,
  preceded by a team lookup that resolves a team key such as `SKUNK` to its id.

A GraphQL client library would be larger than the two documents it would carry,
so the request and response types will be hand-written.

The client will expose three sentinel errors, because the message tables below
need to tell exactly three failure kinds apart:

- `ErrNotFound` — Linear has no such issue. Linear reports a missing entity as a
  GraphQL `errors` entry rather than a null field, so the client must recognize
  that message as well as a null result.
- `ErrUnauthorized` — HTTP 401 or 403.
- `ErrUnreachable` — transport failure, timeout, or non-2xx status.

Every other failure, including a GraphQL `errors` payload and an undecodable
body, is returned as an ordinary wrapped error and reported verbatim.

The HTTP client will carry a 10 second timeout. An unreachable Linear must
degrade quickly rather than stall worktree creation.

## Configuration

ygg has no configuration system today; this feature introduces the first one.

Configuration will live at `config.json` inside `os.UserConfigDir()`, which
resolves to `~/.config/ygg/config.json` on Linux and honors `XDG_CONFIG_HOME`.
It will be decoded with `encoding/json`.

```json
{
  "linear": {
    "defaultTeam": "SKUNK",
    "teams": {
      "GridKitLLC/otter-tools": "SKUNK",
      "GridKitLLC/ygg": "SKUNK"
    }
  }
}
```

JSON is chosen over TOML solely because it costs no dependency. It is less
pleasant to hand-edit, and switching to `BurntSushi/toml` later would touch only
the loader.

Team resolution will proceed in order:

1. The `teams` entry whose key equals the normalized `origin` remote.
2. `defaultTeam`.
3. Neither, in which case ygg warns and proceeds unlinked.

Normalization will reduce a remote URL to `owner/repo`, stripping the scheme,
any `git@host:` prefix, the host, and a trailing `.git`, so that
`git@github.com:GridKitLLC/ygg.git` and
`https://github.com/GridKitLLC/ygg` both produce `GridKitLLC/ygg`. Comparison is
case-sensitive, matching how the hosts themselves are canonicalized.

An absent configuration file is not an error. It leaves every repository
unmapped, which means installing this feature cannot break `ygg new` in
repositories the user has not yet configured.

## Credentials

The API key will be read from the `LINEAR_API_KEY` environment variable and from
nowhere else. Keeping it out of `config.json` keeps a secret out of a file that
may be synchronized between machines or copied into a repository.

An unset key behaves exactly like an outage: a warning, and an unlinked worktree.

## Auto-created Issue Content

- **Title:** the requested name with `-`, `_`, and `/` replaced by spaces and the
  first letter capitalized, so `unified-tui` becomes `Unified tui` and
  `feat/auth` becomes `Feat auth`. This is imperfect for acronyms. Renaming the issue in Linear afterward is expected and does not
  affect the already-created branch, since Linear's branch name is captured at
  creation time.
- **Description:** a single line recording that ygg created the issue and the
  repository it was created for.
- **Team:** the resolved team key.
- **Everything else:** left to Linear's defaults, so the issue lands unassigned
  in the team's default state. Assigning it to the key's owner would require an
  additional `viewer` query and is intentionally omitted.

## Command Integration

`internal/cli/new.go` will gain one helper:

```go
func resolveName(ctx context.Context, svc issueService, team, repo, name string) (branch, note string)
```

`team` is the already-resolved Linear team key and `repo` the already-normalized
`owner/repo`, so `internal/cli` does not depend on the configuration type and the
function stays trivially testable.

It returns no error. Warn-and-proceed is the entire policy of this feature, and
encoding it in the signature makes it structurally impossible for a later change
to introduce a code path where `ygg new` blocks on Linear.

`svc` is a narrow interface satisfied by `*linear.Client`:

```go
type issueService interface {
    Issue(ctx context.Context, identifier string) (*linear.Issue, error)
    CreateIssue(ctx context.Context, teamKey, title, desc string) (*linear.Issue, error)
}
```

Configuration loading happens in `runNew`, not in `resolveName`. The loader
returns `(Config, error)`; on error `runNew` prints the parse warning itself and
passes the zero `Config`, which is indistinguishable from an unmapped repository
and therefore yields an unlinked worktree. This keeps `resolveName` a pure
function of its inputs and keeps the parse warning from having to travel through
a return value that has no room for it.

Likewise, when `LINEAR_API_KEY` is unset, `runNew` passes a nil `svc`.
`resolveName` returns its note without constructing a client or touching the
network.

Notes are printed before the existing `Creating worktree:` line, so the
command's output reads chronologically.

## Messages

Every case below proceeds to create the worktree.

When the requested name is a Linear reference, ygg is only ever confirming
something the user asserted. It must not claim the worktree is unlinked, because
it most likely is linked — ygg simply could not check.

| Reference-shaped name | Message |
| --- | --- |
| Issue found | `Linked to SNK-31 — OWL - have cli also host pure html…` |
| Issue absent | `SNK-13 does not exist in Linear — unlinked` |
| `LINEAR_API_KEY` unset | `No LINEAR_API_KEY set — SNK-31 not verified` |
| Linear unreachable, timeout, or 5xx | `Could not reach Linear — SNK-31 not verified` |
| HTTP 401 or 403 | `Linear rejected the API key — SNK-31 not verified` |
| Any other error | `Could not verify SNK-31: <err>` |

When the requested name is a plain name, ygg is the party responsible for
producing a ticket, so any failure genuinely does leave the worktree unlinked.

| Plain name | Message |
| --- | --- |
| Issue created | `Created SNK-42 — https://linear.app/gridkit/issue/SNK-42/…` |
| `LINEAR_API_KEY` unset | `No LINEAR_API_KEY set — worktree will be unlinked` |
| Repository unmapped, no default team | `No Linear team mapped for GridKitLLC/foo — unlinked` |
| Linear unreachable, timeout, or 5xx | `Could not reach Linear — unlinked` |
| HTTP 401 or 403 | `Linear rejected the API key — unlinked` |
| Creation failed for any other reason | `Could not create Linear issue: <err> — unlinked` |

Independently of either table, a malformed configuration file produces
`Could not parse ~/.config/ygg/config.json: <err>` before resolution begins.

Two of these deserve justification.

A malformed configuration file warns rather than being silently ignored. A
silently skipped typo would leave the user believing the feature is active when
it is not, which is the failure mode this design is least able to detect later.

A rejected key is reported separately from an outage. Collapsing the two would
make a stale or revoked key look like Linear being permanently down, and the
user would never learn to re-mint it.

## Testing

Tests will follow ygg's existing table-driven, hand-rolled-fake style, and will
add no test dependencies.

- **`internal/linear`:** exercised against an `httptest.Server` returning canned
  responses — a successful issue, a successful creation, a GraphQL `errors`
  payload, HTTP 401, HTTP 500, an undecodable body, and a refused connection.
  These pin the mapping from wire responses onto `ErrNotFound`,
  `ErrUnauthorized`, `ErrUnreachable`, and wrapped errors.
- **`resolveName`:** one case per row of the message table, driven by a fake
  `issueService`. This table is the specification of the feature's policy and is
  the test that matters most.
- **Configuration:** remote normalization across `git@host:owner/repo.git`,
  `https://host/owner/repo.git`, and `https://host/owner/repo`; and lookup
  precedence across exact match, default fallback, and neither.
- **Reference detection:** names that do and do not match the pattern, and
  identifier extraction from a full branch name.

`documentation_test.go` currently asserts that `newCmd.Long` mentions Herdr. The
rewritten help text must preserve that mention while describing the Linear
behavior. `README.md` will gain a section covering `LINEAR_API_KEY`, the
configuration file, and the unlinked warnings.

Nothing in the test suite will contact Linear. This is correct for a personal
tool, but it means a change to Linear's API shape would surface when the user
runs `ygg new` rather than when tests run.

## Risks

- **Fork divergence.** This feature has no upstream counterpart in `joch/ygg`.
  Isolating it in `internal/linear` plus a single helper in `new.go` keeps the
  conflict surface to one file that upstream also edits.
- **Unlinked worktrees still accumulate.** Warn-and-proceed means a user who
  ignores warnings ends up where they started. The design accepts this: the
  guarantee it provides is that the configured, online path always produces a
  ticket, not that an unlinked worktree is impossible.
- **Linear's branch name format is a user setting.** The design reads
  `branchName` from the API rather than constructing it, so a user who changes
  the format in Linear gets branches in the new format automatically. One
  format, `{user}/{id}-{title}`, produces a worktree at a nested path such as
  `.worktrees/jeremy/snk-42-x`. `ygg list` derives its displayed name with
  `filepath.Base`, so it would show `snk-42-x` for a branch actually named
  `jeremy/snk-42-x` — the display truncates the leading segment. The impact is
  cosmetic only: `Get` matches a worktree by name or by branch, so `ygg switch`
  and `ygg remove` still work using the full branch name.

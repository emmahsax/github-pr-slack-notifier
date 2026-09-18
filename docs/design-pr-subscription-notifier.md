---
title: PR Subscription Slack Notifier
date_created: 2026-08-31
last_updated: 2026-09-18
owner: emmahsax (personal project)
authored_by: Claude Code (Anthropic AI coding agent), directed interactively by the owner
tags: [design, architecture, infrastructure, github, slack, lambda]
---

> **Note:** This specification was drafted by an AI coding agent (Claude Code) working interactively with the repository owner throughout the design and implementation process, not hand-written from scratch by a human. Treat it accordingly — verify anything load-bearing against the actual code rather than assuming the doc is authoritative on its own.

# Introduction

This specification describes a personal, self-owned tool for Slack notifications on GitHub pull request (PR) activity, built to replace a third-party subscription-based tool the user's employer was discontinuing. The user relies heavily on this kind of notification and needs an equivalent that is cheap to run, fully portable across employers/AWS accounts, and discoverable/killable by teammates if the user is offboarded without warning.

## 1. Purpose & Scope

This document specifies the requirements, subscription/notification semantics, architecture, and ownership model for a system that watches GitHub pull requests across one or more configured GitHub organizations and sends the user a Slack notification when a PR they are "subscribed" to (per the rule in Section 3) receives specific activity.

**Audience**: anyone deploying or extending this system — the author, or any future implementer (human or AI) building it from scratch.

**Assumptions**:
- The implementer has access to create a GitHub App under the user's personal GitHub account.
- The implementer has access to an AWS account and, if deploying into a company's shared account, that company's existing Terraform conventions.
- The user has a Slack workspace in which to receive notifications.
- The implementer has access to create a Slack App under the company's Slack organization.

## 2. Definitions

- **PR**: GitHub Pull Request.
- **Draft PR**: A PR with GitHub's `draft` flag set to `true`; not yet ready for review.
- **Ready-for-review PR**: A PR with `draft: false`.
- **Subscription**: A user-specific, per-PR state indicating the user should be notified of further activity on that PR. Defined precisely in Section 3.
- **GitHub App**: A GitHub integration identity (distinct from a personal user account or Personal Access Token) that can be installed on one or more GitHub organizations/repositories with scoped permissions, and can be independently suspended or uninstalled.
- **Installation Access Token**: A short-lived token issued to a GitHub App for a specific installation, used to call the GitHub REST/GraphQL API.
- **Lambda Function URL**: An AWS Lambda feature that exposes a Lambda function directly over HTTPS without requiring API Gateway.
- **Thin consumer block**: A minimal Terraform `module` block that references an external module by git source and version tag, supplying only the inputs needed for a specific deployment, with no business logic duplicated locally.

## 3. Requirements, Constraints & Guidelines

### Subscription Rule

- **REQ-001**: A PR is "subscribed" for the user if and only if the user has taken at least one of the following explicit actions on that specific PR: (a) authored it, (b) pushed at least one commit to it, (c) submitted at least one review on it (any review state), (d) posted at least one comment on it (issue comment or review comment).
- **REQ-002**: Being requested as a reviewer on a PR does NOT, by itself, create a subscription.
- **REQ-003**: Being @mentioned in a PR's title, body, or any comment does NOT, by itself, create a subscription.
- **REQ-004**: Subscription state MUST be determined by querying live GitHub API/data at the time of an incoming event. No separate persistent subscription database is required or should be introduced.

### Notification Triggers

- **REQ-005**: Notifications MUST only be generated for PRs where `draft: false` at the time of the triggering event. Events on draft PRs MUST be ignored.
- **REQ-006**: For a subscribed, non-draft PR, the system MUST notify the user on each of the following events:
  - A new comment is posted (issue comment or PR review comment).
  - A review is submitted with state "approved".
  - A review is submitted with state "changes requested".
  - The PR is merged.
  - A label is added to the PR.
  - A label is removed from the PR.
- **CON-001**: A review submitted with state "commented" is treated as equivalent to "a new comment is posted" (its own REQ-006 bullet) since it carries the same kind of freeform feedback text, even though it isn't literally one of REQ-006's listed events — EXCEPT when that review's body is empty (or whitespace-only). GitHub creates a "commented" review with no body whenever someone adds one or more inline diff comments without also writing a top-level review summary; each inline comment already gets its own notification (see the `pull_request_review_comment` row in Section 4), so a bare, contentless "commented" review in that case MUST NOT also notify. This exception applies only to the "commented" state — "approved" and "changes requested" MUST still notify even with an empty body, since the verdict itself is the content.
- **REQ-007**: A PR transitioning from draft to ready-for-review MUST NOT itself be a notification trigger (only listed in REQ-006 events, evaluated only once non-draft).
- **REQ-008**: The user being requested for review, or @mentioned, MUST NOT independently trigger a notification, per REQ-002/REQ-003 (no subscription exists) — this holds even though notifying on review-requests and mentions is common behavior in similar commercial tools, which this system deliberately does NOT replicate.
- **REQ-011**: REQ-006 triggers are NOT restricted to open PRs. A merged PR remains eligible for further notifications (e.g., a comment posted after merge) as long as it is subscribed and non-draft — merging does not end a subscription or exempt the PR from future trigger events.
- **REQ-012**: Authorship alone (REQ-001a) is sufficient subscription for the "PR is merged" trigger (REQ-006) — an author MUST be notified when their own PR is merged, independent of whether they also reviewed, commented, or committed further.
- **REQ-019**: For a subscribed, non-draft PR, the system MUST notify the user when a new commit is pushed to the PR's head branch (GitHub's `pull_request` `synchronize` action), UNLESS the pusher is the user themself, per the same self-notification exclusion already applied to comments (handleIssueComment/handlePullRequestReviewComment) and reviews (handlePullRequestReview). Unlike REQ-012's merge exception, there is no "notify regardless of who pushed" carve-out here — a self-push is always suppressed. The pusher is identified via the webhook payload's top-level `sender`, the same field GitHub uses for every other event this system already keys self-exclusion off of; the payload does not enumerate individual pushed commits' authors, so a force-push or a squash containing commits from multiple people is still attributed to whoever performed the push.
- **REQ-020**: For a non-draft PR, the system MUST notify the user when the PR is reopened (`pull_request` `reopened` action), but ONLY if a Slack thread already exists for that PR — not via REQ-001's normal authored/committed/reviewed/commented check. An existing thread means the user was already notified about this PR before (so they were already subscribed by definition); no thread means the user was never subscribed, and reopening alone MUST NOT newly subscribe them. This is checked regardless of who reopened it — no self-notification exclusion, matching REQ-012's/labeling's precedent rather than the comment/review/commit self-exclusion precedent.
- **REQ-021**: For a non-draft PR, the system MUST notify the user when the PR is closed without merging (`pull_request` `closed` action with `merged: false`), under the identical thread-existence condition and no-self-exclusion rule as REQ-020. A merge is also delivered as a `closed` action (with `merged: true`) but is already covered by REQ-011/REQ-012's separate "merged" trigger — REQ-021 MUST NOT additionally fire for a merge, to avoid double-notifying the same close event.
- **CON-006**: REQ-020/REQ-021's thread-existence check is only meaningful for bot-token delivery (REQ-015/REQ-016), since only that delivery method has a thread store. For incoming-webhook delivery, reopening or closing a PR MUST be a silent no-op, consistent with REQ-018's treatment of the same delivery method for title changes.

### Scope / Configuration

- **REQ-009**: The set of GitHub organizations watched MUST be configurable (a list).
- **REQ-010**: The system is single-user — it notifies only the deploying user. Multi-user support is an explicit non-goal at this time (see Section 12).

### Cost & Portability Constraints

- **CON-002**: The running system MUST NOT require always-on compute or paid always-on services. Only pay-per-use AWS services (Lambda, Function URL, and — for bot-token/threaded delivery — DynamoDB on-demand) are permitted.
- **CON-003**: The system MUST be redeployable to a new AWS account and a new GitHub App/Slack workspace with minimal rework — limited to swapping credentials/config and re-running Terraform, not rewriting application logic.
- **CON-004**: The Terraform module MUST NOT read secrets from any specific backend itself (see GUD-005). Whatever backend a consumer uses to store secrets, avoid AWS Secrets Manager's per-secret monthly charge if choosing one for a new deployment (SSM Parameter Store `SecureString` is free and sufficient).

### Security / Kill-Switch

- **SEC-001**: The GitHub App MUST be registered under the user's personal GitHub account, not any organization account, so that the user retains independent control (suspend/uninstall) regardless of their org membership status.
- **SEC-002**: Incoming webhook requests to the Lambda Function URL MUST be authenticated by verifying the GitHub webhook HMAC signature (`X-Hub-Signature-256`) before any processing.
- **SEC-003**: The deployment into any shared/employer-owned AWS account MUST be discoverable and destroyable by a third party (see GUD-004) without requiring the user's involvement.
- **SEC-004**: The GitHub App MUST request the minimum permission scope needed (read-only access to pull requests, issues/comments, and repository metadata) and subscribe only to the webhook events needed (`pull_request`, `pull_request_review`, `pull_request_review_comment`, `issue_comment`). Omitting `pull_request_review_comment` specifically means GitHub never delivers inline diff comments (`#discussion_r...` URLs) or their thread replies at all — not a processing bug, a missing subscription; there is no delivery to debug in the GitHub App's "Recent Deliveries" log when this happens, which is itself the diagnostic signal.

### Ownership & Repo Structure

- **GUD-001**: The portable artifact (Lambda handler code, GitHub App manifest/setup documentation, and a generic parameterized Terraform module) MUST live in a new repository under the user's personal GitHub namespace, independent of any employer's repositories. This is the canonical, reusable source.
- **GUD-002**: The Terraform module in the personal repo MUST be generic and parameterized (GitHub org allowlist, AWS deployment target, Slack credentials, etc. as input variables) with no employer-specific values hardcoded.
- **GUD-003**: Any deployment into an employer-owned AWS account MUST be done via a "thin consumer block" in that employer's Terraform repository, referencing the personal repo's module by git source and a pinned version tag. The employer repo's copy does not require duplicating module logic.
- **GUD-004**: The thin consumer block deployed into a shared employer account MUST carry a tag or description clearly identifying it as the user's personal tool, stating it is safe to destroy on the user's offboarding, and linking to the personal repo (e.g., `Description = "Personal PR-notification Lambda for <user> -- safe to destroy on offboarding, code at github.com/<user>/<repo>"`). The module also tags every taggable resource with `Description`, `ManagedBy`, `Name`, `Owner`, `Region`, and `ResourceType` — `Name`/`ResourceType` are always derived from the actual resource and can never be overridden; `Description`/`ManagedBy`/`Owner`/`Region` all default automatically but can be overridden, all through the single `tags` variable (GUD-007).
- **GUD-005**: The module MUST accept secret material (`github.app_private_key`, `github.webhook_secret`, `slack.credential`) as direct sensitive input variables, not as references to a specific secrets backend (e.g. not as SSM parameter names). This keeps the module portable across whatever secrets-management convention a given consumer already uses — resolving a name/path to an actual value (via an SSM `data` source, a secrets-manager-backed variable, or otherwise) is the consumer's responsibility.
- **GUD-006**: Every AWS resource name (`function_name`, and per-resource overrides `lambda_function_name`/`iam_role_name`/`thread_store_policy_name`/`thread_store_table_name`) MUST default to a value that includes `github_username`, so multiple people's instances of this module can coexist in the same AWS account without a name collision — while still being individually overridable. IAM resource names default to PascalCase, everything else to dash-case.
- **GUD-007**: All tag customization MUST go through a single `tags` input variable (a map). The module computes sensible defaults for `Description`/`ManagedBy`/`Owner`/`Region` internally and merges them with whatever the consumer supplies in `tags`, so a consumer overrides a default by setting the same key (e.g. `tags = { Owner = "someone-else" }`). Every value in `tags` MUST be validated at plan time against the AWS tag-value character set (letters, numbers, spaces, and `. : / = + - @`), since AWS tag values reject other punctuation such as commas.
- **GUD-008**: The module MUST resolve which Lambda code to deploy via exactly three priority tiers, evaluated in this order: (1) `lambda_source.zip_path` (a local file path, default `"../../dist/lambda.zip"`) resolves to an existing local file — always wins, since it's an explicit "I have a build I'm actively testing" signal; (2) `zip_path` does not resolve to an existing file, and `lambda_source.github_release_version` (a git tag string, default `null`) is non-null — the module downloads that tag's `lambda.zip` GitHub Release asset from `lambda_source.github_release_repo` (an `"owner/repo"` string, defaulting to this repo's own canonical location but overridable — GUD-001/PAT-001 already establish that forks are an expected, supported way to run this codebase, so this must not be hardcoded to one specific repo) and deploys it; (3) `zip_path` does not resolve AND `github_release_version` is `null` — fall back to whatever `source_code_hash` is already deployed (via a data source lookup), making `plan`/`apply` a silent no-op. `github_release_repo`, `github_release_version`, and `zip_path` are grouped into one `lambda_source` object variable (GUD-009) — `github_release_repo` has no effect without `github_release_version` being set, and `zip_path`/`github_release_version` are compared against each other on every apply to decide which tier is active. Tiers (1) and (2) MUST mirror their winning bytes to one fixed, machine-independent local path (rather than `aws_lambda_function` referencing `zip_path` or a per-version release path directly) — the AWS provider only re-reads and re-uploads code when `filename` or `source_code_hash` differs from state, so a stable, tier-independent `filename` value is what makes tier (3) an actual no-op requiring no file to exist. `lifecycle.ignore_changes` on `filename` is not a viable substitute for this: it would freeze `filename` to whatever value is currently recorded in state, permanently, regardless of which tier is active — blocking every real deploy (including tiers (1) and (2)) once that frozen value stops being a readable file, since the AWS provider still tries to re-read `filename` for any update to the resource, for any reason at all. Note tier (3)'s condition is not "neither field is set" — `zip_path` always holds some value (its own default or an override) whether or not a file actually exists there; it's the file's non-existence, not the field's absence, that matters. `github_release_version` MUST NOT attempt to auto-detect itself from the module call's own `source = "...?ref=<tag>"` — Terraform has no mechanism for a module to introspect its own source/ref meta-arguments. Keeping the two in sync is the consumer's responsibility, and it MUST be done as two independent literal values, not a single shared value — `source`'s ref is a true compile-time-only string (Terraform resolves a module's source address during `terraform init`, before any locals or variables are evaluated), so unlike every other input this module accepts, it cannot be interpolated from a `local` at all. A consumer who tries `source = "...?ref=${local.version}"` gets a hard "Variables not allowed" error.
- **GUD-009**: Related Terraform input variables MUST be grouped into a single object variable when they are only ever meaningful/used together. `lambda_source` bundles `github_release_repo`/`github_release_version`/`zip_path` (GUD-008) since all three feed the same three-tier precedence decision. `github` bundles `app_id`/`app_private_key`/`webhook_secret` — each referenced in exactly one place (`lambda.tf`'s environment block) and nowhere else, a tight single-purpose credential bundle. `slack` bundles `credential`/`delivery_method`/`target` — `target`'s relevance depends on `delivery_method`'s value (required for `"bot_token"`, unused for `"incoming_webhook"`), the same `optional()`-in-object pattern `lambda_source.github_release_repo`/`zip_path` use. `github_username`/`github_org_allowlist` deliberately stay as separate top-level variables rather than joining `github` — `github_username` is load-bearing in `locals.tf` for the function-name default and two auto-generated tags (`Description`, `Owner`), a module-wide identity value rather than an App credential, and `github_org_allowlist` is deployment-scope config, conceptually separate from App credentials. Both `github` and `slack` MUST be declared `sensitive = true` as a whole, since `app_private_key`/`webhook_secret`/`credential` are real secrets and Terraform cannot mark sensitivity per-attribute within a single object-typed variable — this also hides the non-sensitive sibling attributes (`app_id`, `delivery_method`, `target`) from plan output, an accepted tradeoff in favor of never risking a secret leaking into plan/apply output. `lambda_source` has no such tradeoff to make, since none of its fields are sensitive.
- **PAT-001**: Duplicating or copying Terraform configuration between repositories (rather than referencing a single versioned module) is explicitly disallowed — it causes drift and violates the single-canonical-source convention a mature Terraform repo should already have.
- **CON-005**: Deployment into an employer's Terraform repository MUST follow that repository's existing safety process in full (e.g., mandated review tooling, atomic single-state PRs, human-run-only `apply`/`destroy`) — the specific process is whatever that repository already requires; this spec doesn't prescribe one.

## 4. Interfaces & Data Contracts

### Inbound: GitHub Webhook → Lambda Function URL

Delivered by the GitHub App installation. Relevant event types and the fields the handler depends on:

| Event | Trigger condition consumed | Key fields used |
|---|---|---|
| `issue_comment` | `action == "created"`, `issue.pull_request` present | `issue.pull_request.url`, `issue.draft` (requires a follow-up PR fetch), `comment.user.login`, `repository.full_name` |
| `pull_request_review_comment` | `action == "created"` | `pull_request.draft` (already present, no fetch needed), `comment.user.login`, `comment.body`. Covers both a thread's first inline comment and any replies within it — GitHub delivers both identically, with no field distinguishing a reply from a new thread. Requires the `pull_request_review_comment` webhook subscription (SEC-004) in addition to the events above; a missing subscription here produces no delivery at all, not a processing error. |
| `pull_request_review` | `action == "submitted"`, `review.state in {approved, changes_requested, commented}` (CON-001) | `pull_request.draft`, `review.state`, `review.user.login` |
| `pull_request` | `action == "closed"` and `pull_request.merged == true` | `pull_request.draft`, `pull_request.merged`, `pull_request.merged_by.login` |
| `pull_request` | `action == "labeled"` | `pull_request.draft`, `label.name` |
| `pull_request` | `action == "unlabeled"` | `pull_request.draft`, `label.name` |
| `pull_request` | `action == "synchronize"` | `pull_request.draft`, `sender.login`, `after` (new head commit SHA), `repository.html_url` (used to build the commit permalink: `{repository.html_url}/commit/{after}`) |
| `pull_request` | `action == "reopened"` | `pull_request.draft`; subscription determined by thread-store lookup (REQ-020), not the fields listed under Section 3's outbound REST calls |
| `pull_request` | `action == "closed"`, `pull_request.merged == false` | `pull_request.draft`; subscription determined by thread-store lookup (REQ-021), same as "reopened" |
| `pull_request` | `action == "edited"`, `changes.title` present | `pull_request.title`, `pull_request.html_url` (REQ-018 thread header refresh, not a notification) |

All handlers MUST discard the event early if `pull_request.draft == true` (or, for `issue_comment`, if the referenced PR is a draft — requires fetching the PR object since `issue_comment` payloads do not include `draft`).

### Outbound: GitHub REST API (via Installation Access Token)

Used to compute subscription state (REQ-001) at event time:

| Purpose | Endpoint (REST) |
|---|---|
| List PR commits (check commit authorship) | `GET /repos/{owner}/{repo}/pulls/{pr}/commits` |
| List PR reviews | `GET /repos/{owner}/{repo}/pulls/{pr}/reviews` |
| List PR issue comments | `GET /repos/{owner}/{repo}/issues/{pr}/comments` |
| List PR review comments | `GET /repos/{owner}/{repo}/pulls/{pr}/comments` |
| Fetch PR (for draft status / author) | `GET /repos/{owner}/{repo}/pulls/{pr}` |

### Outbound: Slack

- **REQ-013**: The system MUST support both Slack delivery mechanisms, selectable via configuration, not a single hardcoded choice:
  - **Bot token** (`chat.postMessage`) — required for threading (REQ-015) and for DM delivery.
  - **Incoming webhook URL** — required when the user prefers routing to a channel without managing a Slack App/bot, at the cost of no threading (REQ-017).
- **REQ-014**: Exactly one delivery mechanism MUST be configured per deployment (mutually exclusive, not both simultaneously) — whichever secret/config is present (bot token vs. webhook URL) determines which delivery path the Lambda uses.
- **REQ-015**: With bot-token delivery, all notifications for the same PR (identified by owner/repo/number) MUST be grouped into a single Slack thread. The first notification-worthy event for a PR MUST create the thread by posting a generic header — `owner/repo#number (@author): title` — and every notification for that PR, including that first one, MUST be posted as a plain reply (`@sender action`) under that header. In the header, the portion up to and including the colon (`owner/repo#number (@author):`) MUST be rendered in Slack mrkdwn bold (`*...*`), with the title after it unstyled. In each reply, only the `@sender` mention MUST be bold; the action verb and any freeform detail (comment/review body preview) after it MUST stay unstyled.
- **REQ-016**: Thread state (PR → Slack header message timestamp) MUST be persisted externally to the Lambda (not in-memory), since Lambda execution environments don't reliably persist state between invocations. This does not conflict with REQ-004's "no persistent subscription database" — that requirement is about subscription determination, which is unrelated bookkeeping.
- **REQ-017**: Threading (REQ-015) is only possible with bot-token delivery, since only `chat.postMessage` returns a message timestamp to reply against — Slack incoming webhooks never return one. Incoming-webhook delivery MUST instead post one flat message per notification, containing the same header line as the first line and the event as a Slack blockquote (`> @sender action`) on the line beneath it, with the same bold formatting as REQ-015, e.g. (rendered in Slack):
  ```
  *my-org/my-repo#1299 (@emmahsax):* Add widget support
  > *@reviewer1* commented: Looks good to me
  ```
- **REQ-018**: When a PR's title changes (a GitHub `pull_request` webhook with `action: "edited"` and a `changes.title` key — GitHub omits keys in `changes` for anything that didn't change, so this is how a title-specific edit is distinguished from other edits like a base-branch change), and a Slack thread already exists for that PR (bot-token delivery only — see REQ-015/REQ-017), the system MUST update that thread's header message in place (via Slack's `chat.update`) to reflect the new title. This requires no subscription check and sends no new message — only the existing header's text changes. If no thread exists yet for that PR, or delivery is incoming-webhook, this MUST be a silent no-op — with incoming-webhook delivery specifically, this means previously sent messages are NEVER retroactively rewritten; each REQ-017 flat message reflects only the title at the moment that specific message was sent, and a later title change only affects the next message sent, not any already-delivered ones.

### Configuration Contract (Terraform module inputs — as implemented)

```hcl
variable "github" {
  type = object({
    app_id          = string
    app_private_key = string
    webhook_secret  = string
  })
  description = "The GitHub App's numeric ID, PEM-encoded RSA private key, and webhook secret (used to verify inbound signatures), grouped since they're only ever used together (GUD-009)."
  sensitive   = true
}

variable "github_org_allowlist" {
  type        = list(string)
  description = "GitHub organizations this deployment watches."
}

variable "github_username" {
  type        = string
  description = "GitHub login whose subscriptions are evaluated."
}

variable "slack" {
  type = object({
    credential      = string
    delivery_method = string
    target          = optional(string, "")
  })
  description = "credential: the Slack bot token or incoming webhook URL, whichever delivery_method selects. delivery_method: either \"bot_token\" or \"incoming_webhook\" — selects which Slack delivery path is used. target: Slack channel or user ID (for bot_token delivery); unused for incoming_webhook delivery. Grouped since target's relevance depends on delivery_method (GUD-009)."
  sensitive   = true
}
```

Per GUD-005, secret values (`github.app_private_key`, `github.webhook_secret`, `slack.credential`) are passed as direct sensitive Terraform variables — the module does not read them from SSM or any other backend itself. A consumer resolves them however its own Terraform setup manages secrets before passing the value in.

## 5. Acceptance Criteria

- **AC-001**: Given a non-draft PR the user authored, When a new comment is posted on it by someone else, Then the user receives a Slack notification.
- **AC-002**: Given a non-draft PR the user has never authored, committed to, reviewed, or commented on, When someone requests the user for review, Then no Slack notification is sent.
- **AC-003**: Given a non-draft PR the user has never authored, committed to, reviewed, or commented on, When someone @mentions the user in a comment, Then no Slack notification is sent.
- **AC-004**: Given a PR the user previously reviewed, When the PR is later merged, Then the user receives a Slack notification, regardless of who merged it.
- **AC-005**: Given a draft PR the user authored, When a comment is posted on it, Then no Slack notification is sent.
- **AC-006**: Given a PR the user authored that is converted from draft to ready-for-review, When a label is subsequently added, Then the user receives a Slack notification (draft status is evaluated at the time of the labeling event, not at PR creation).
- **AC-007**: Given a PR in an organization not in `github_org_allowlist`, When any triggering event occurs on it, Then no Slack notification is sent (the GitHub App is not installed there, so no event is even received).
- **AC-008**: Given the GitHub App is suspended or uninstalled, When any PR activity occurs, Then no webhook is delivered and no notification is sent (kill-switch verification).
- **AC-009**: Given a webhook request without a valid `X-Hub-Signature-256` matching the configured secret, When it is received by the Lambda Function URL, Then the request is rejected and no GitHub API calls are made.
- **AC-010**: Given a non-draft PR the user authored, When the PR is merged, Then the user receives a Slack notification, regardless of who merged it and regardless of whether the user also reviewed, committed, or commented.
- **AC-011**: Given a PR the user is subscribed to (e.g., they authored or reviewed it) that has already been merged, When a new comment is posted on it afterward, Then the user receives a Slack notification (subscription and non-draft status persist past merge).
- **AC-012**: Given bot-token delivery and a PR with no existing thread, When the first notification-worthy event for that PR arrives, Then a generic header message is posted first (no `thread_ts`), followed by the event itself as a plain reply threaded under that header — the header text does not vary based on which event triggered it.
- **AC-013**: Given bot-token delivery and a PR with an existing thread, When a second notification-worthy event for that same PR arrives, Then no new header is posted — only a plain reply threaded under the existing header.
- **AC-014**: Given incoming-webhook delivery, When any notification-worthy event arrives, Then exactly one flat message is sent containing the header line followed by the event as a blockquoted (`> `) line, with no separate header message ever sent.
- **AC-015**: Given bot-token delivery and a PR with an existing thread, When the PR's title is edited, Then the thread's header message is updated in place via `chat.update` and no new message is sent to the channel.
- **AC-016**: Given a PR with no existing thread (regardless of delivery method), When the PR's title is edited, Then nothing is sent to Slack at all — no error, no message.
- **AC-017**: Given a PR is edited but the title itself did not change (e.g., only the base branch changed), When the webhook arrives, Then no header update and no notification occurs.
- **AC-018**: Given a non-draft PR the user is subscribed to, When someone else posts an inline diff comment (`pull_request_review_comment`, `action: "created"`) — whether it's the first comment in a new review thread or a reply within an existing one — Then the user receives a Slack notification, formatted identically to an `issue_comment` notification.
- **AC-019**: Given a non-draft PR the user is subscribed to, When someone else submits a review with state `"commented"` (CON-001), Then the user receives a Slack notification formatted the same way as a plain comment (`@sender commented: <body preview>`), not a distinct "approved"/"changes requested" wording and not silently ignored.
- **AC-020**: Given a non-draft PR the user is subscribed to, When a label is removed from it, Then the user receives a Slack notification, symmetric with AC for label addition.
- **AC-021**: Given a non-draft PR the user is subscribed to, When someone else submits a review with state `"commented"` and an empty or whitespace-only body (CON-001), Then no Slack notification is sent.
- **AC-022**: Given a non-draft PR the user is subscribed to, When someone else submits a review with state `"approved"` or `"changes requested"` and an empty body, Then the user still receives a Slack notification — the empty-body exception in CON-001 applies only to the "commented" state.
- **AC-023**: Given a non-draft PR the user is subscribed to, When someone else pushes a new commit to it (`pull_request` `synchronize`), Then the user receives a Slack notification whose link points to that commit's GitHub HTML URL (`{repository.html_url}/commit/{after}`), not the PR itself.
- **AC-024**: Given a draft PR, When a commit is pushed to it, Then no Slack notification is sent (REQ-005/REQ-019).
- **AC-025**: Given a non-draft PR, When the user themself pushes a commit to it, Then no Slack notification is sent, regardless of whether the user authored the PR (REQ-019's self-push exclusion has no merge-style "notify anyway" carve-out).
- **AC-026**: Given bot-token delivery, a non-draft PR, and an existing Slack thread for it, When the PR is reopened, Then the user receives a Slack notification, regardless of who reopened it.
- **AC-027**: Given bot-token delivery and a PR with no existing Slack thread, When the PR is reopened, Then no Slack notification is sent (the user was never subscribed, and reopening alone doesn't create a subscription).
- **AC-028**: Given bot-token delivery and an existing Slack thread for a PR, When the PR is reopened directly into a draft state, Then no Slack notification is sent (REQ-005/REQ-020).
- **AC-029**: Given bot-token delivery, a non-draft PR, and an existing Slack thread for it, When the PR is closed without merging, Then the user receives a Slack notification, regardless of who closed it.
- **AC-030**: Given bot-token delivery and a PR with no existing Slack thread, When the PR is closed without merging, Then no Slack notification is sent.
- **AC-031**: Given bot-token delivery and an existing Slack thread for a PR, When the PR is merged (`closed` with `merged: true`), Then exactly one notification is sent (the merge notification per REQ-011/REQ-012) — REQ-021's close trigger MUST NOT also fire for the same event.
- **AC-032**: Given incoming-webhook delivery, When a PR is reopened or closed without merging, Then no Slack message is sent at all, regardless of thread-equivalent state, since incoming-webhook delivery has no thread store (CON-006).

## 6. Test Automation Strategy

- **Test Levels**: Unit tests for the subscription-determination logic and event-filtering logic (pure functions, mockable GitHub API responses); integration test(s) against a real or sandboxed GitHub repository/App installation to validate end-to-end webhook delivery and Slack posting; no UI, so no end-to-end browser testing applies.
- **Frameworks**: Standard Go `testing` package with table-driven tests, plus `net/http/httptest` for fake GitHub API and Slack API servers — no additional test framework needed.
- **Test Data Management**: Use recorded/fixture GitHub webhook payloads (from GitHub's documented example payloads or captured from a personal test repo) rather than live org data.
- **CI/CD Integration**: Personal repo should run unit tests on push via GitHub Actions (free tier for personal repos).
- **Coverage Requirements**: No formal threshold mandated; prioritize coverage of the subscription rule (REQ-001–REQ-004) and draft-filtering (REQ-005) since these are the highest-risk-of-silent-bug areas.
- **Performance Testing**: Not applicable at this scale (single user, low PR volume).

## 7. Rationale & Context

- The previous tool providing this functionality is being canceled at the employer level; the user relies on it heavily and needs continuity.
- A **webhook-driven** design was chosen over a scheduled poller because GitHub webhook payloads directly identify the triggering action (comment/review/merge/label), avoiding the need to track "since last check" state and diff API responses — this is both simpler to implement and gives near-instant delivery instead of poll-interval latency.
- A **GitHub App owned by the user personally** (rather than a Personal Access Token, and rather than an App owned by the employer org) was chosen specifically so the kill-switch is independent of the user's org membership: if the user is offboarded, org access revocation alone does not require anyone to remember to also revoke this tool's access — the user can suspend the App themselves at any time, and a teammate can also uninstall it from the org's installed-GitHub-Apps settings without needing the user's cooperation.
- **No persistent subscription database** was chosen to keep the system stateless and cheap — subscription state is cheap to compute on-demand per event (a handful of GitHub API calls scoped to one PR) and avoids operating a database for a single-user, low-volume tool.
- The **repo-split (personal repo for code/module, thin consumer block in the employer repo)** balances two competing needs: full portability/ownership (the user keeps the real logic in something they own forever) against team visibility (the employer repo's copy is where teammates already look for "what's running in this AWS account," and Terraform state/PR history there provides the audit trail needed for someone else to find and destroy the resource if the user cannot).
- The module is **secrets-backend-agnostic** (GUD-005) rather than reading directly from a specific backend like SSM, since different consumers may already have their own secrets-management convention — forcing one specific backend would mean some consumers need to create throwaway resources in that backend just to satisfy the module's interface, instead of passing an already-resolved value straight through.
- **Lambda Function URL over API Gateway** was selected purely to minimize baseline cost, consistent with CON-002.
- **arm64 (Graviton2) over amd64/x86_64** for the Lambda's architecture — arm64 is roughly 20% cheaper per GB-second than x86_64 for equivalent Lambda workloads, on top of AWS's claimed better price-performance, making it the cheaper choice for CON-002's cost-minimization goal.
- **`lambda_source.github_release_version` (module self-downloads its own release) over a CI pre-apply hook** (e.g. Spacelift/Terrateam/etc `before_plan` shelling out to `curl`) for getting code onto non-interactive runners: a hook lives entirely outside this repo's Terraform, in whatever pipeline-specific config format the consumer's CI platform uses, so it can't be versioned, tested, or discovered alongside the module itself, and its pinned release URL has no structural link to the module's own `source` ref — nothing stops the two from silently drifting apart. A hook also hides real code changes: bumping only the module's `source` ref, with no local build and no CI-staged zip, changes nothing about `source_code_hash` (Section 4's data-source fallback returns whatever's already deployed), so a CI plan can report "no changes" even though the Lambda's code did change. `lambda_source.github_release_version` avoids this by making the release itself a real, typed Terraform input living in the same module block as `source`, rather than a raw string buried in separate CI-platform config — this doesn't eliminate the need to manually keep `lambda_source.github_release_version` and `source`'s ref in sync (Terraform's `source` argument can't reference a variable at all, so no mechanism can fully automate this — see GUD-008), but same-block proximity makes drift far easier to notice than a hook living in an entirely different file and config format. The cost is adding `hashicorp/http`/`hashicorp/local` as provider dependencies and the state-size/plan-time cost noted in PLT-003.

## 8. Dependencies & External Integrations

### External Systems
- **EXT-001**: GitHub (github.com) — source of all PR/review/comment/label/merge events, via a GitHub App installation and its REST API.
- **EXT-002**: Slack — notification delivery target, via either a bot token or an incoming webhook.

### Third-Party Services
- **SVC-001**: GitHub Apps platform — provides webhook delivery, installation access tokens, and independent suspend/uninstall control (no additional SLA required beyond GitHub's own).

### Infrastructure Dependencies
- **INF-001**: AWS Lambda — hosts the event handler; must support a Function URL.
- **INF-002**: A secrets backend of the consumer's choosing (e.g. AWS SSM Parameter Store `SecureString` parameters, or another Terraform-managed secret store) — resolves to the plaintext values the module's `github.app_private_key`, `github.webhook_secret`, and `slack.credential` inputs expect (GUD-005). Not a dependency of the module itself.
- **INF-003**: Whatever existing Terraform tooling and state backend a consumer already uses — needed only for the thin consumer block, not for the module itself.
- **INF-004**: AWS DynamoDB (on-demand billing) — required only for bot-token delivery, to persist PR → Slack-thread-timestamp state across Lambda invocations (REQ-016). Not created at all for incoming-webhook delivery, which has no thread state to persist.

### Data Dependencies
- None beyond live GitHub API responses fetched per-event; no data warehousing or batch data dependency exists.

### Technology Platform Dependencies
- **PLT-001**: AWS Lambda runtime — Go, compiled to a static `arm64` binary and run via the `provided.al2023` custom runtime (no first-class managed Go runtime exists).
- **PLT-002**: Terraform — the personal module should avoid provider-version pins tighter than necessary, to stay usable across different consumers' Terraform versions.
- **PLT-003**: `hashicorp/http` (>= 3.3.0, for binary-safe `response_body_base64`) and `hashicorp/local` (>= 2.0.0, for `local_file`'s `content_base64sha256`) — the module's only non-`aws` provider dependencies. `required_providers` is a static declaration Terraform resolves at `terraform init` time, independent of the `count` gating their actual usage (GUD-008) — every consumer downloads and locks both providers regardless of whether `lambda_source.github_release_version` is ever set, not just the ones who use it. The larger, genuinely conditional cost applies whenever tier (1) or tier (2) (GUD-008) is actually active — a real local build at `lambda_source.zip_path`, or `lambda_source.github_release_version` set — and splits into three distinct effects rather than one. First, whichever tier is active mirrors its zip's bytes into a `local_file` (GUD-008), growing Terraform state size by roughly the zip's size; on a workspace where that `local_file` persists between applies (e.g. a developer's own machine), an apply with unchanged content is a genuine no-op — state holds a current snapshot, not a history, so new bytes simply replace the stored value rather than adding a second copy alongside it. Second, that no-op assumption depends entirely on the mirrored file surviving between applies, which it does NOT on an ephemeral runner: Spacelift and most CI platforms start every run from a fresh checkout, and the mirrored file is gitignored, so on those platforms it's always missing at the start of a plan. `local_file`'s computed hash is unknown until the provider actually writes the file, so Terraform proposes — and performs — an update to `aws_lambda_function` on every single run in that environment, re-uploading identical bytes, regardless of whether the underlying content changed. This effect is specific to `lambda_source.github_release_version` in practice, since `zip_path` only takes effect when a real local build genuinely exists on disk, which normally isn't the case on the same ephemeral runners. Third, `lambda_source.github_release_version` specifically also costs a real network fetch on every single `plan`/`apply` where it's set, even when it hasn't changed, since Terraform data sources re-fetch on every plan rather than caching — this recurs independent of whether the second effect above ends up mattering. A local build via `lambda_source.zip_path` doesn't re-fetch anything (it's a plan-time file read, not a data source). A consumer with a versioned state backend (e.g. S3 bucket versioning) is a further exception where storage does genuinely accumulate across applies even in the non-ephemeral case, since each apply's full-state snapshot bakes in that zip-sized entry even though the live state file itself stays flat. Expect a `Warning: Response body is not recognized as UTF-8` on every `plan`/`apply` where `lambda_source.github_release_version` is set — harmless, not a correctness signal. `hashicorp/http` always populates its plain-string `response_body` attribute internally and warns when it isn't valid UTF-8 regardless of which attribute is actually read; this module only ever reads `response_body_base64` (binary-safe), so the warning doesn't reflect any problem with the downloaded content.

### Compliance Dependencies
- None identified. This is a personal productivity tool operating only on the user's own PR activity metadata; it does not process other users' PII beyond GitHub usernames already visible in PR metadata.

## 9. Examples & Edge Cases

**Edge case — "committed but never commented"**: A user pushes a commit to a co-authored PR opened by someone else, then never comments or reviews it. Detecting this subscription case requires listing the PR's commits and checking each commit's author/committer identity against the user — already covered by the "List PR commits" API call in Section 4. This only becomes materially harder if the number of commits on a PR is large enough to require pagination, which is a straightforward implementation detail, not an architectural blocker.

**Edge case — draft PR converted to ready-for-review, then immediately labeled**: The `pull_request` `ready_for_review` action itself does not trigger a notification (REQ-007), but if a label is added afterward in a separate event, REQ-005/REQ-006 evaluate `draft` at that later event's time and correctly notify.

```json
// Example: pull_request_review webhook payload (trimmed) used to evaluate REQ-006
{
  "action": "submitted",
  "review": {
    "state": "changes_requested",
    "user": { "login": "someone-else" }
  },
  "pull_request": {
    "draft": false,
    "user": { "login": "emmahsax" },
    "number": 42
  },
  "repository": { "full_name": "my-org/example-repo" }
}
```

## 10. Validation Criteria

- All acceptance criteria in Section 5 pass against recorded/fixture webhook payloads.
- A manual end-to-end test: install the GitHub App on a personal test repository, open a PR, comment/review/label/merge it, and confirm Slack delivery matches Section 3's rules exactly, including negative cases (draft PRs, mention-only, review-request-only).
- Suspending or uninstalling the GitHub App immediately stops all notification delivery (AC-008), verified manually before relying on it as a real kill-switch.
- The Terraform consumer block in an employer's repository plans/applies cleanly through that repository's normal review process, with no changes required inside the personal module itself.

## 11. Related Specifications / Further Reading

- GitHub Apps documentation: webhook event types, JWT-to-installation-token exchange flow — consult GitHub's own developer docs directly.

## 12. Future Improvements (Not Implemented)

Known gaps, deliberately deferred rather than built into v1. Each includes why it was deferred, so a future implementer can judge whether the tradeoff still holds.

- **FUT-001: Renewal / dead-man's-switch mechanism.** The kill-switch design (SEC-001, SEC-003) assumes *someone* — the user themself, or a remaining org admin — knows to go uninstall the GitHub App or destroy the Terraform-managed resources. If the user is offboarded without notice and nobody else is aware this tool exists, webhook processing and Slack notifications could continue indefinitely with no one prompted to check. A renewal mechanism would require the deploying user to periodically confirm they're still around (e.g., a scheduled reminder they must acknowledge, or a small CLI/script that bumps a "last renewed" timestamp in a datastore); a scheduled check (e.g. daily EventBridge-triggered Lambda) would compare that timestamp against a threshold (e.g. 30–45 days) and, if exceeded, either alert a shared channel or automatically disable notification delivery. **Deferred because**: the tool's blast radius is low (read-only GitHub access, Slack-only output, negligible AWS cost — an unnoticed instance is a hygiene issue, not a security incident), and the immediate mitigation adopted instead was informing a teammate/manager of the tool's existence and teardown steps out-of-band, so the auto-generated resource tags (GUD-004) have at least one other person primed to act on them. Worth revisiting if this pattern is reused for a tool with a larger blast radius.
- **FUT-002: Multi-user support.** Currently single-user (REQ-010) — notifies exactly one GitHub username per deployment. Supporting more than one user would require, at minimum, iterating the subscription check per configured user and either multiple Slack targets or a shared channel with per-user @-mentions.

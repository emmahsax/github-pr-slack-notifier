---
title: PR Subscription Slack Notifier (Pullflow Replacement)
version: 1.0
date_created: 2026-08-31
owner: emmahsax (personal project)
tags: [design, architecture, infrastructure, github, slack, lambda]
---

# Introduction

This specification describes a personal, self-owned replacement for Pullflow's Slack notification feature. Pullflow's subscription is being canceled by the user's employer. The user relies heavily on Slack notifications for pull request (PR) activity and needs an equivalent that is cheap to run, fully portable across employers/AWS accounts, and discoverable/killable by teammates if the user is offboarded without warning.

## 1. Purpose & Scope

This document specifies the requirements, subscription/notification semantics, architecture, and ownership model for a system that watches GitHub pull requests across one or more configured GitHub organizations and sends the user a Slack notification when a PR they are "subscribed" to (per the rule in Section 3) receives specific activity.

**Audience**: the user (emmahsax) and any future implementer (human or AI) building this system from scratch.

**Assumptions**:
- The implementer has access to create a GitHub App under the user's personal GitHub account.
- The implementer has access to an AWS account (initially the employer's shared development account) and the employer's Terraform repository conventions.
- The user has a Slack workspace (initially the employer's) in which to receive notifications.
- Out of scope: replicating any other Pullflow feature (e.g., PR analytics, stale-PR reminders, team dashboards). This spec covers only the Slack-notification-on-subscribed-PR-activity feature.

## 2. Definitions

- **PR**: GitHub Pull Request.
- **Draft PR**: A PR with GitHub's `draft` flag set to `true`; not yet ready for review.
- **Ready-for-review PR**: A PR with `draft: false`.
- **Subscription**: A user-specific, per-PR state indicating the user should be notified of further activity on that PR. Defined precisely in Section 3.
- **GitHub App**: A GitHub integration identity (distinct from a personal user account or Personal Access Token) that can be installed on one or more GitHub organizations/repositories with scoped permissions, and can be independently suspended or uninstalled.
- **Installation Access Token**: A short-lived token issued to a GitHub App for a specific installation, used to call the GitHub REST/GraphQL API.
- **Lambda Function URL**: An AWS Lambda feature that exposes a Lambda function directly over HTTPS without requiring API Gateway.
- **SSM Parameter Store**: AWS Systems Manager's key-value configuration/secret storage service; `SecureString` parameters are encrypted at rest via KMS.
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
- **CON-001**: Review submissions with state "commented" (a review left without approval/changes-requested) are NOT an explicit trigger in REQ-006, but MAY be treated as equivalent to "a new comment is posted" since it carries comment text. This ambiguity is intentional — see OQ-003.
- **REQ-007**: A PR transitioning from draft to ready-for-review MUST NOT itself be a notification trigger (only listed in REQ-006 events, evaluated only once non-draft).
- **REQ-008**: The user being requested for review, or @mentioned, MUST NOT independently trigger a notification, per REQ-002/REQ-003 (no subscription exists) — this holds even though these are the two Pullflow behaviors this system explicitly does NOT replicate.
- **REQ-011**: REQ-006 triggers are NOT restricted to open PRs. A merged PR remains eligible for further notifications (e.g., a comment posted after merge) as long as it is subscribed and non-draft — merging does not end a subscription or exempt the PR from future trigger events.
- **REQ-012**: Authorship alone (REQ-001a) is sufficient subscription for the "PR is merged" trigger (REQ-006) — an author MUST be notified when their own PR is merged, independent of whether they also reviewed, commented, or committed further.

### Scope / Configuration

- **REQ-009**: The set of GitHub organizations watched MUST be configurable (a list).
- **REQ-010**: The system is single-user in its initial version — it notifies only the deploying user. Multi-user support is an explicit non-goal for v1 (see OQ-005).

### Cost & Portability Constraints

- **CON-002**: The running system MUST NOT require always-on compute or paid always-on services. Only pay-per-use AWS services (Lambda, Function URL, and — for bot-token/threaded delivery — DynamoDB on-demand) are permitted.
- **CON-003**: The system MUST be redeployable to a new AWS account and a new GitHub App/Slack workspace with minimal rework — limited to swapping credentials/config and re-running Terraform, not rewriting application logic.
- **CON-004**: The Terraform module MUST NOT read secrets from any specific backend itself (superseded from an earlier "SSM Parameter Store only" requirement — see GUD-005). Whatever backend a consumer uses to store secrets, avoid AWS Secrets Manager's per-secret monthly charge if choosing one for a new deployment (SSM Parameter Store `SecureString` is free and sufficient).

### Security / Kill-Switch

- **SEC-001**: The GitHub App MUST be registered under the user's personal GitHub account, not any organization account, so that the user retains independent control (suspend/uninstall) regardless of their org membership status.
- **SEC-002**: Incoming webhook requests to the Lambda Function URL MUST be authenticated by verifying the GitHub webhook HMAC signature (`X-Hub-Signature-256`) before any processing.
- **SEC-003**: The deployment into any shared/employer-owned AWS account MUST be discoverable and destroyable by a third party (see GUD-001) without requiring the user's involvement.
- **SEC-004**: The GitHub App MUST request the minimum permission scope needed (read-only access to pull requests, issues/comments, and repository metadata) and subscribe only to the webhook events needed (`pull_request`, `pull_request_review`, `pull_request_review_comment`, `issue_comment`). Omitting `pull_request_review_comment` specifically means GitHub never delivers inline diff comments (`#discussion_r...` URLs) or their thread replies at all — not a processing bug, a missing subscription; there is no delivery to debug in the GitHub App's "Recent Deliveries" log when this happens, which is itself the diagnostic signal.

### Ownership & Repo Structure

- **GUD-001**: The portable artifact (Lambda handler code, GitHub App manifest/setup documentation, and a generic parameterized Terraform module) MUST live in a new repository under the user's personal GitHub namespace, independent of any employer's repositories. This is the canonical, reusable source.
- **GUD-002**: The Terraform module in the personal repo MUST be generic and parameterized (GitHub org allowlist, AWS deployment target, Slack credentials, etc. as input variables) with no employer-specific values hardcoded.
- **GUD-003**: Any deployment into an employer-owned AWS account MUST be done via a "thin consumer block" in that employer's Terraform repository, referencing the personal repo's module by git source and a pinned version tag. The employer repo's copy does not require duplicating module logic.
- **GUD-004**: The thin consumer block deployed into a shared employer account MUST carry a tag or description clearly identifying it as the user's personal tool, stating it is safe to destroy on the user's offboarding, and linking to the personal repo (e.g., `Description = "Personal PR-notification Lambda for <user> -- safe to destroy on offboarding, code at github.com/<user>/<repo>"`). The module also tags every taggable resource with `Description`, `ManagedBy`, `Name`, `Owner`, `Region`, and `ResourceType`, matching the tagging convention already established across the user's own `terraform` repo (`github.com/<user>/terraform`) — `Name`/`ResourceType` are always derived from the actual resource and can never be overridden; `Description`/`ManagedBy`/`Owner`/`Region` all default automatically but can be overridden, all through the single `tags` variable (GUD-007) — not one variable per tag.
- **GUD-007**: All tag customization MUST go through a single `tags` input variable (a map), not a separate variable per tag key. The module computes sensible defaults for `Description`/`ManagedBy`/`Owner`/`Region` internally and merges them with whatever the consumer supplies in `tags`, so a consumer overrides a default by setting the same key (e.g. `tags = { Owner = "someone-else" }`) rather than needing to discover and set a same-purpose dedicated variable. Every value in `tags` MUST be validated against the AWS tag-value character set (letters, numbers, spaces, and `. : / = + - @`) at plan time, not just whichever key happened to cause a past failure — this was a deliberate generalization after the comma-in-`Description` bug (see Section 9) broke `DynamoDB`/`IAM` tag creation at apply time; validating only `Description` would have left the same failure mode reachable through any other tag key.
- **GUD-006**: Every AWS resource name (`function_name`, and per-resource overrides `lambda_function_name`/`iam_role_name`/`thread_store_policy_name`/`thread_store_table_name`) MUST default to a value that includes `github_username`, so multiple people's instances of this module can coexist in the same AWS account without a name collision — while still being individually overridable. IAM resource names default to PascalCase, everything else to dash-case, matching `github.com/<user>/terraform`'s established split.
- **PAT-001**: Duplicating or copying Terraform configuration between repositories (rather than referencing a single versioned module) is explicitly disallowed — it was considered and rejected because it causes drift and violates the single-canonical-source convention already established in the employer's Terraform repo (`AGENTS.md`).
- **CON-005**: Deployment into the employer's Terraform repository MUST follow that repository's existing safety process in full: use of the mandated `terraform-architect` agent for `.tf` changes, atomic single-state PRs, and human-run (never agent-run) `apply`/`destroy`. Initial deployment target: `workloads-sdlc-development` (Tier 1 "eng" per that repo's criticality tiers).
- **GUD-005**: The module MUST accept secret material (`github_app_private_key`, `github_webhook_secret`, `slack_credential`) as direct sensitive input variables, not as references to a specific secrets backend (e.g. not as SSM parameter names). This keeps the module portable across whatever secrets-management convention a given consumer already uses — resolving a name/path to an actual value (via an SSM `data` source, reading a SOPS-encrypted tfvars-sourced variable, or otherwise) is the consumer's responsibility. Revised from an earlier version of this spec that had the module read directly from SSM Parameter Store by name; that assumption didn't hold once deploying into a Terraform repo (the employer's) whose own convention is SOPS-encrypted tfvars, not SSM.

## 4. Interfaces & Data Contracts

### Inbound: GitHub Webhook → Lambda Function URL

Delivered by the GitHub App installation. Relevant event types and the fields the handler depends on:

| Event | Trigger condition consumed | Key fields used |
|---|---|---|
| `issue_comment` | `action == "created"`, `issue.pull_request` present | `issue.pull_request.url`, `issue.draft` (may require a follow-up PR fetch), `comment.user.login`, `repository.full_name` |
| `pull_request_review_comment` | `action == "created"` | `pull_request.draft` (already present, no fetch needed), `comment.user.login`, `comment.body`. Covers both a thread's first inline comment and any replies within it — GitHub delivers both identically, with no field distinguishing a reply from a new thread. Requires the `pull_request_review_comment` webhook subscription (SEC-004) in addition to the events above; a missing subscription here produces no delivery at all, not a processing error. |
| `pull_request_review` | `action == "submitted"`, `review.state in {approved, changes_requested}` (and optionally `commented`, see OQ-003) | `pull_request.draft`, `review.state`, `review.user.login` |
| `pull_request` | `action == "closed"` and `pull_request.merged == true` | `pull_request.draft`, `pull_request.merged`, `pull_request.merged_by.login` |
| `pull_request` | `action == "labeled"` | `pull_request.draft`, `label.name` |
| `pull_request` | `action == "edited"`, `changes.title` present | `pull_request.title`, `pull_request.html_url` (REQ-018 thread header refresh, not a notification) |

All handlers MUST discard the event early if `pull_request.draft == true` (or, for `issue_comment`, if the referenced PR is a draft — requires fetching the PR object since `issue_comment` payloads do not include `draft`).

### Outbound: GitHub REST/GraphQL API (via Installation Access Token)

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
  - **Bot token** (`chat.postMessage`) to a Slack user ID — required for DM delivery.
  - **Incoming webhook URL** — required when the user prefers routing to a channel instead of a DM, or wants to avoid managing a Slack App/bot.
- **REQ-014**: Exactly one delivery mechanism MUST be configured per deployment (mutually exclusive, not both simultaneously) — whichever secret/config is present (bot token vs. webhook URL) determines which delivery path the Lambda uses.
- **REQ-015**: With bot-token delivery, all notifications for the same PR (identified by owner/repo/number) MUST be grouped into a single Slack thread. The first notification-worthy event for a PR MUST create the thread by posting a generic header — `owner/repo#number (@author): title`, not tied to whichever specific event triggered it — and every notification for that PR, including that first one, MUST be posted as a plain reply (`@sender action`) under that header, never as its own top-level message. In both the header and each reply, the portion up to and including the colon (`owner/repo#number (@author):` / `@sender action:`) MUST be rendered in Slack mrkdwn bold (`*...*`); the freeform detail after the colon (title, or comment/review body preview) MUST stay unstyled. When there is no freeform detail (e.g. a merge or label event), the entire line is bold instead of only the part before a colon, since there is no colon.
- **REQ-016**: Thread state (PR → Slack header message timestamp) MUST be persisted externally to the Lambda (not in-memory), since Lambda execution environments don't reliably persist state between invocations. This does not conflict with REQ-004's "no persistent subscription database" — that requirement is about subscription determination, which is unrelated bookkeeping.
- **REQ-017**: Threading (REQ-015) is only possible with bot-token delivery, since only `chat.postMessage` returns a message timestamp to reply against — Slack incoming webhooks never return one. Incoming-webhook delivery MUST instead post one flat message per notification, containing the same header line as the first line and the event as a Slack blockquote (`> @sender action`) on the line beneath it, with the same bold formatting as REQ-015, e.g. (rendered in Slack):
  ```
  *my-org/my-repo#1299 (@emmahsax):* Add widget support
  > *@reviewer1 commented:* Looks good to me
  ```
- **REQ-018**: When a PR's title changes (a GitHub `pull_request` webhook with `action: "edited"` and a `changes.title` key — GitHub omits keys in `changes` for anything that didn't change, so this is how a title-specific edit is distinguished from other edits like a base-branch change), and a Slack thread already exists for that PR (bot-token delivery only — see REQ-015/REQ-017), the system MUST update that thread's header message in place (via Slack's `chat.update`) to reflect the new title. This is not itself a notification: no subscription check applies, and no new message is sent — only the existing header's text changes. If no thread exists yet for that PR, or delivery is incoming-webhook, this MUST be a silent no-op — with incoming-webhook delivery specifically, this means previously sent messages are NEVER retroactively rewritten; each REQ-017 flat message reflects only the title at the moment that specific message was sent, and a later title change only affects the next message sent, not any already-delivered ones.

### Configuration Contract (Terraform module inputs — as implemented)

```hcl
variable "github_app_id" {
  type        = string
  description = "The GitHub App's numeric ID."
}

variable "github_app_private_key" {
  type        = string
  description = "The GitHub App's PEM-encoded RSA private key."
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

variable "github_webhook_secret" {
  type        = string
  description = "The GitHub App's webhook secret, used to verify inbound signatures."
  sensitive   = true
}

variable "slack_credential" {
  type        = string
  description = "The Slack bot token or incoming webhook URL, whichever slack_delivery_method selects."
  sensitive   = true
}

variable "slack_delivery_method" {
  type        = string
  description = "Either \"bot_token\" or \"incoming_webhook\" — selects which Slack delivery path is used."
}

variable "slack_target" {
  type        = string
  description = "Slack channel or user ID (for bot_token delivery); unused for incoming_webhook delivery."
}
```

Per GUD-005, secret values (`github_app_private_key`, `github_webhook_secret`, `slack_credential`) are passed as direct sensitive Terraform variables — the module does not read them from SSM or any other backend itself. A consumer resolves them however its own Terraform setup manages secrets (an SSM `data` source, a SOPS-encrypted-tfvars-sourced variable, etc.) before passing the value in.

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

## 6. Test Automation Strategy

- **Test Levels**: Unit tests for the subscription-determination logic and event-filtering logic (pure functions, mockable GitHub API responses); integration test(s) against a real or sandboxed GitHub repository/App installation to validate end-to-end webhook delivery and Slack posting; no UI, so no end-to-end browser testing applies.
- **Frameworks**: To be chosen alongside the Lambda runtime/language (open question, see OQ-002).
- **Test Data Management**: Use recorded/fixture GitHub webhook payloads (from GitHub's documented example payloads or captured from a personal test repo) rather than live org data.
- **CI/CD Integration**: Personal repo should run unit tests on push via GitHub Actions (free tier for personal repos).
- **Coverage Requirements**: No formal threshold mandated; prioritize coverage of the subscription rule (REQ-001–REQ-004) and draft-filtering (REQ-005) since these are the highest-risk-of-silent-bug areas.
- **Performance Testing**: Not applicable at this scale (single user, low PR volume).

## 7. Rationale & Context

- Pullflow is being canceled at the employer level; the user relies on it heavily and needs continuity.
- A **webhook-driven** design was chosen over a scheduled poller because GitHub webhook payloads directly identify the triggering action (comment/review/merge/label), avoiding the need to track "since last check" state and diff API responses — this is both simpler to implement and gives near-instant delivery instead of poll-interval latency.
- A **GitHub App owned by the user personally** (rather than a Personal Access Token, and rather than an App owned by the employer org) was chosen specifically so the kill-switch is independent of the user's org membership: if the user is offboarded, org access revocation alone does not require anyone to remember to also revoke this tool's access — the user can suspend the App themselves at any time, and a teammate can also uninstall it from the org's installed-GitHub-Apps settings without needing the user's cooperation.
- **No persistent subscription database** was chosen to keep the system stateless and cheap — subscription state is cheap to compute on-demand per event (a handful of GitHub API calls scoped to one PR) and avoids operating a database for a single-user, low-volume tool.
- The **repo-split (personal repo for code/module, thin consumer block in the employer repo)** balances two competing needs: full portability/ownership (the user keeps the real logic in something they own forever) against team visibility (the employer repo's copy is where teammates already look for "what's running in this AWS account," and Terraform state/PR history there provides the audit trail needed for someone else to find and destroy the resource if the user cannot).
- **Making the module secrets-backend-agnostic** (GUD-005) rather than baking in an SSM-specific interface was a late revision, prompted by deploying into the employer's Terraform repo — that repo's own convention is SOPS-encrypted tfvars, not SSM, so a module hardcoded to expect SSM parameter names would have forced an unnecessary extra layer (creating throwaway SSM parameters just to satisfy the module's interface) instead of passing already-decrypted values straight through. SSM Parameter Store remains the recommended choice *when a consumer needs to pick a backend from scratch*, over Secrets Manager, purely to minimize baseline cost (CON-004).
- Lambda Function URL over API Gateway was selected purely to minimize baseline cost, consistent with CON-002.
- **arm64 (Graviton2) over amd64/x86_64** for the Lambda's architecture — arm64 is roughly 20% cheaper per GB-second than x86_64 for equivalent Lambda workloads, on top of AWS's claimed better price-performance, so it's the cheaper choice, not the more expensive one (this was raised as a possible switch during design and confirmed as already-optimal, not changed).

## 8. Dependencies & External Integrations

### External Systems
- **EXT-001**: GitHub (github.com) — source of all PR/review/comment/label/merge events, via a GitHub App installation and its REST/GraphQL API.
- **EXT-002**: Slack — notification delivery target, via either a bot token or an incoming webhook.

### Third-Party Services
- **SVC-001**: GitHub Apps platform — provides webhook delivery, installation access tokens, and independent suspend/uninstall control (no additional SLA required beyond GitHub's own).

### Infrastructure Dependencies
- **INF-001**: AWS Lambda — hosts the event handler; must support a Function URL.
- **INF-002**: A secrets backend of the consumer's choosing (e.g. AWS SSM Parameter Store `SecureString` parameters, or SOPS-encrypted tfvars) — resolves to the plaintext values the module's `github_app_private_key`, `github_webhook_secret`, and `slack_credential` inputs expect (GUD-005). Not a dependency of the module itself.
- **INF-003**: Employer's existing Terraform/Terragrunt tooling and state backend (S3, per that repo's conventions) — used only for the thin consumer block, not for the module itself.

### Data Dependencies
- None beyond live GitHub API responses fetched per-event; no data warehousing or batch data dependency exists.

### Technology Platform Dependencies
- **PLT-001**: AWS Lambda runtime — language/runtime not yet chosen (see OQ-002).
- **PLT-002**: Terraform — version should match whatever the consuming employer repo's Terragrunt setup requires; the personal module itself should avoid provider-version pins tighter than necessary to stay portable across employers' Terraform versions.

### Compliance Dependencies
- None identified. This is a personal productivity tool operating only on the user's own PR activity metadata; it does not process other users' PII beyond GitHub usernames already visible in PR metadata.

## 9. Examples & Edge Cases

**Edge case — "committed but never commented"**: A user pushes a commit to a co-authored PR opened by someone else, then never comments or reviews it. Detecting this subscription case requires listing the PR's commits and checking each commit's author/committer identity against the user — already covered by the "List PR commits" API call in Section 4, so no additional commit-search API call should be necessary in the common case. (This resolves what was flagged as a possible complication — it only becomes materially harder if the number of commits on a PR is large enough to require pagination, which is a straightforward implementation detail, not an architectural blocker.)

**Edge case — draft PR converted to ready-for-review, then immediately labeled**: The `pull_request` `ready_for_review` action itself does not trigger a notification (REQ-007), but if a label is added afterward in a separate event, REQ-005/REQ-006 evaluate `draft` at that later event's time and correctly notify.

**Edge case — review left with state "commented"**: Ambiguous per CON-001; see OQ-003.

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
- The Terraform consumer block in the employer repo plans/applies cleanly through that repo's normal `terraform-architect` agent and PR review process, with no changes required inside the personal module itself.

## 11. Related Specifications / Further Reading

- Employer Terraform repository conventions: `~/dev/terraform/AGENTS.md` (canonical instructions doc; defines the external-module-reference pattern this design follows, and the criticality-tier/atomic-PR safety rules this deployment must follow).
- GitHub Apps documentation: webhook event types, JWT-to-installation-token exchange flow (external; not linked here per this document's no-guessed-URLs constraint — implementer should consult GitHub's own developer docs directly).
- Open questions to resolve before/during implementation:
  - ~~OQ-001: Slack delivery mechanism~~ — resolved: support both, selectable via `slack_delivery_method` (REQ-013/REQ-014).
  - ~~OQ-002: Lambda language/runtime choice~~ — resolved: Go, chosen specifically to minimize ongoing maintenance (static binary, standard-library-only for JWT/HTTP, no `go-github`, no JWT library — the only third-party dependencies are `aws-lambda-go` and, for thread persistence, `aws-sdk-go-v2`'s DynamoDB client).
  - ~~OQ-003: Whether a "commented" review state counts~~ — resolved: yes, treated as comment-equivalent (implemented in `internal/handler.handlePullRequestReview`).
  - ~~OQ-004: GitHub App permission scope~~ — resolved: Pull requests (read-only), Issues (read-only, for issue comments), Metadata (read-only); webhook events `issue_comment`, `pull_request_review`, `pull_request`. See README "Setting up the GitHub App."
  - **OQ-005**: Config format for the org allowlist is resolved (comma-separated `GITHUB_ORG_ALLOWLIST` env var / `github_org_allowlist` Terraform list variable). Multi-user support remains open — would require, at minimum, iterating subscription checks per configured user and either multiple Slack targets or a shared channel with per-user @-mentions.

## 12. Future Improvements (Not Implemented)

These are known gaps, deliberately deferred rather than built into v1. Each includes why it was deferred, so a future implementer can judge whether the tradeoff still holds.

- **FUT-001: Renewal / dead-man's-switch mechanism.** The kill-switch design (SEC-001, SEC-003) assumes *someone* — the user themself, or a remaining org admin — knows to go uninstall the GitHub App or destroy the Terraform-managed resources. If the user is offboarded without notice and nobody else is aware this tool exists, webhook processing and Slack notifications could continue indefinitely with no one prompted to check. A renewal mechanism would require the deploying user to periodically confirm they're still around (e.g., a scheduled reminder they must acknowledge, or a small CLI/script that bumps a "last renewed" timestamp in SSM or DynamoDB); a scheduled check (e.g. daily EventBridge-triggered Lambda) would compare that timestamp against a threshold (e.g. 30–45 days) and, if exceeded, either alert a shared channel or automatically disable notification delivery (e.g. by making the handler short-circuit, or by revoking the Function URL). **Deferred because**: the tool's blast radius is low (read-only GitHub access, Slack-only output, negligible AWS cost — an unnoticed instance is a hygiene issue, not a security incident), and the immediate mitigation adopted instead was informing a teammate/manager of the tool's existence and teardown steps out-of-band, so the auto-generated resource tags (GUD-004) have at least one other person primed to act on them. Worth revisiting if this pattern is reused for a tool with a larger blast radius, or if a general dead-man's-switch utility already exists elsewhere that this could plug into instead of building bespoke.
- ~~FUT-002: Refresh the thread header when the PR title changes~~ — implemented: see REQ-018.

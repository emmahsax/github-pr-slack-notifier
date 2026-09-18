# GitHub PR → Slack Notifier

A project designed to provide Slack notifications when GitHub PRs change: get pinged in Slack when a pull request you're **"subscribed"** to gets a comment, an approval, a changes-requested review, a merge, a new commit, or a label added/removed — but only for PRs that are ready for review (not draft), and only when you took an explicit prior action on that PR (authored it, pushed a commit, reviewed it, or commented on it). Being requested for review or @mentioned does *not* count as "subscribed". New commits pushed by you yourself never notify you, even on your own PR.

Full requirements and design rationale: [`docs/design-pr-subscription-notifier.md`](docs/design-pr-subscription-notifier.md).

## How it works

A GitHub App (registered under your **personal** GitHub account) delivers webhook events straight to an AWS Lambda via a Lambda Function URL. The Lambda verifies the webhook signature, checks the PR isn't a draft, computes subscription live off the GitHub API (no database required for that part), and if you're "subscribed", posts to Slack via either a bot token or an incoming webhook.

Registering the App under your own account (not a GH org) means you can suspend or uninstall it yourself at any time, independent of your org membership — a real kill switch if you're ever offboarded without a chance to do it yourself.

With bot-token delivery, all of a PR's notifications are grouped into a single Slack thread: the first time a PR gets a notification-worthy event, the bot posts a generic header message (`*owner/repo#number (@author):* PR title` — bold up to the colon, title plain) to start the thread, then posts the actual event as a plain reply underneath it (`*@sender* action: detail` — only the `@sender` mention is bold); every later event for that same PR replies into that same thread, and the header is posted only once (see "Notes and known limitations" for what that means if the PR is later renamed). This needs a small DynamoDB table to remember each PR's thread timestamp across Lambda invocations.

Incoming-webhook delivery can't thread (Slack's incoming webhooks never return a message timestamp to reply against), so every notification is a single flat message with the same header text as the first line and the event blockquoted underneath, e.g. (rendered in Slack — `*text*` is mrkdwn bold):

```
*my-org/my-repo#1299 (@emmahsax):* Add widget support
> *@reviewer1* commented: Looks good to me
```

## Repo layout

- `cmd/lambda/` — the Lambda entrypoint.
- `internal/webhook/` — GitHub webhook signature verification.
- `internal/githubapp/` — GitHub App JWT auth, installation-token exchange, and the small slice of the GitHub REST API this needs (no `go-github` dependency, standard library only).
- `internal/slack/` — Slack delivery (bot token or incoming webhook), including thread replies.
- `internal/threadstore/` — DynamoDB-backed lookup of which Slack thread a PR's notifications belong to.
- `internal/config/` — environment-variable configuration loading.
- `internal/handler/` — ties the above together and implements the notification rules.
- `terraform/module/` — a generic, parameterized Terraform module that deploys the Lambda (and, for bot-token delivery, the DynamoDB table). Nothing employer-specific is hardcoded here; a consuming repo supplies all the specifics (see "Deploying" below).

## Setting up the GitHub App

1. Create a new GitHub App under your **personal** account (Settings → Developer settings → GitHub Apps → New GitHub App), not under any organization. Note, the name of the app must be unique across _ALL_ of GitHub.
2. Make the homepage URL the link to this GitHub repository.
2. Permissions (repository, read-only unless noted):
   - Pull requests: Read-only
   - Issues: Read-only (issue comments live under this permission)
   - Metadata: Read-only (required by default)
3. Subscribe to webhook events: `Issue comment`, `Pull request review`, `Pull request review comment`, `Pull request`. `Pull request review comment` is easy to miss — without it, inline diff comments (GitHub's `#discussion_r...` URLs, as opposed to `#issuecomment-...`) and their thread replies never reach the Lambda at all (not a bug to debug — GitHub simply never sends the delivery if the App isn't subscribed).
4. Webhook URL: the Lambda Function URL, once deployed (see "Deploying"). You can also point it at a placeholder and update it after the first `terraform apply`.
5. Generate and download a webhook secret and a private key (`.pem`), and take note of the App ID. The Terraform module takes these as a direct sensitive input object (`github = { app_id, app_private_key, webhook_secret }`) rather than reading them from any particular backend itself — store them however your Terraform setup already manages secrets (SSM Parameter Store, encrypted tfvars, etc.) and resolve them to values before passing them to the module. If using SSM, I recommend prefixing parameter names with your GitHub username to keep instances distinct if this is ever deployed for more than one person in the same account, e.g. `/github-pr-slack-notifier/<username>/github-app-private-key`.
6. Install the App on whichever GitHub organization(s)/repositories you want notifications from.

## Slack setup

Pick one delivery method:

- **Bot token** (`slack.delivery_method = "bot_token"`), recommended if you want threading:
  1. Create a Slack channel for your PR notifications (e.g. `#emmahsax-prs`).
  2. Create a Slack App with a bot token scoped to `chat:write`, install it to your workspace, and invite the bot user into that channel.
  3. Get the channel's ID (right-click the channel → View channel details, or `#channel-name` in a browser URL bar resolves to a `C...` ID) and set `slack.target` to it.
- **Incoming webhook** (`slack.delivery_method = "incoming_webhook"`): create a Slack incoming webhook for whichever channel you want notifications in. Simpler to set up, but every message is flat (no threading — see "How it works").

Same as the GitHub secrets: pass the resulting token/URL to the module as `slack.credential`, sourced however your Terraform setup manages secrets.

## Building Locally

```sh
task build   # produces dist/lambda.zip (arm64, provided.al2023)
task test    # go test ./...
```

## Deploying

This module is meant to be consumed by reference (a git source + version tag), not copied. Grab a tagged release in this repo and either download the `lambda.zip` from GitHub or build it yourself locally. Then you can call the existing Terraform module in your AWS Terraform config — for example, a thin block in a company's shared infra repo, or your own personal one.

The module itself never talks to SSM (or any other secrets backend) — it just takes plaintext values. If you're using SSM, resolve them in your own consumer config first:

```hcl
data "aws_ssm_parameter" "github_app_private_key" {
  name            = "/github-pr-slack-notifier/emmahsax/github-app-private-key"
  with_decryption = true
}

data "aws_ssm_parameter" "github_webhook_secret" {
  name            = "/github-pr-slack-notifier/emmahsax/github-webhook-secret"
  with_decryption = true
}

data "aws_ssm_parameter" "slack_credential" {
  name            = "/github-pr-slack-notifier/emmahsax/slack-credential"
  with_decryption = true
}

module "pr_slack_notifier" {
  # The ref must be a literal string here — Terraform resolves a module's source
  # address during `terraform init`, before any locals/variables are
  # evaluated, so interpolation is never allowed in `source`, not even from
  # a local. Update this by hand to match lambda_source.github_release_version below when
  # bumping versions; nothing enforces the two staying in sync.
  source = "git::https://github.com/emmahsax/github-pr-slack-notifier.git//terraform/module?ref=v0.0.6"

  github = {
    app_id          = "123456"
    app_private_key = data.aws_ssm_parameter.github_app_private_key.value
    webhook_secret  = data.aws_ssm_parameter.github_webhook_secret.value
  }

  github_org_allowlist = ["my-org"]
  github_username      = "emmahsax"

  # Optional — see "Providing the Lambda's code" below. Omit lambda_source
  # entirely if you don't have a local build and don't want the module
  # fetching one itself.
  lambda_source = {
    # Defaults to this repo's own emmahsax/github-pr-slack-notifier —
    # only set it if you're running from a fork.
    github_release_repo = "emmahsax/github-pr-slack-notifier"

    # Keep this matching source's ref above, by hand.
    github_release_version = "v0.0.6"

    zip_path = "${path.module}/../../dist/lambda.zip"
  }

  slack = {
    credential      = data.aws_ssm_parameter.slack_credential.value
    delivery_method = "bot_token"
    target          = "C0123456789"
  }
}
```

If your Terraform setup already manages secrets some other way (e.g. encrypted tfvars), skip the data sources and pass those values into the module directly instead.

### Providing the Lambda's code

`lambda_source` is not **required** — most people planning/applying this module need neither of its fields set, only whoever is actually pushing a Lambda code update. The module resolves the code to deploy in this priority order, highest first:

1. **`lambda_source.zip_path`** resolves to a real local file (default `../../dist/lambda.zip`, i.e. `task build`'s output). This always wins — it's the explicit "I'm actively changing the code and testing it" signal, so it overrides everything else even if `lambda_source.github_release_version` is also set.
2. **`lambda_source.github_release_version`** is set and `zip_path` didn't resolve to a file. The module downloads that tag's `lambda.zip` release asset itself from `lambda_source.github_release_repo` (defaults to this repo's own `emmahsax/github-pr-slack-notifier` — override it if you're running from a fork, otherwise you'll silently deploy someone else's build) via the `hashicorp/http` and `hashicorp/local` providers, and deploys that — no local build, no CI pipeline step, no pre-apply hook required. This is the option for non-interactive runners (Spacelift, Atlantis, CI-driven `apply`, Terrateam, etc) and for teammates who just want "whatever the pinned version is" without building anything.
3. **`lambda_source.zip_path` doesn't resolve to a file, and `lambda_source.github_release_version` is `null`** (its default). Note this isn't "neither field is set" — `zip_path` always has *some* value (its own default, or an explicit override) whether or not a file actually exists there; what puts you in this tier is that path not resolving, not the version being unset. The module falls back to whatever code is already deployed, so `plan`/`apply` is a clean no-op — this works because options 1 and 2 both mirror their bytes to the same fixed internal path (see `lambda_deploy.tf`) before `aws_lambda_function` ever sees them, so that path's value never changes between applies regardless of which option last won or which machine/runner applied it; combined with this tier's `source_code_hash` matching whatever's already deployed, neither attribute the AWS provider watches for a code update ever actually differs, so it never re-reads any file, whether or not one exists on disk. This is the default for most consumer files, and is also what a *first-ever* apply falls back to if it has neither a local build nor `github_release_version` set — with no existing function yet, that first apply will fail, so a genuinely fresh deployment needs one of the first two options at least once.

### Additional Costs and Notes

The module depends on `hashicorp/http` and `hashicorp/local` in addition to `aws` — every consumer's `terraform init` downloads and locks both, whether or not `lambda_source.github_release_version` is ever set, since Terraform resolves provider requirements statically rather than based on whether the resources using them end up with `count = 0`.

When either `lambda_source.zip_path` or `lambda_source.github_release_version` is set:
- Either one mirrors the zip's bytes into Terraform state (roughly the zip's size, currently a few MB). Assuming the local mirror file from a previous apply is still present (see the next point for when it isn't), an apply with no code changes doesn't change the state's size — a new local build or a new `github_release_version` replaces the previous bytes rather than adding to them. If your state backend keeps historical snapshots (e.g. S3 bucket versioning), each apply's full-state snapshot still bakes in that zip-sized entry, so storage there can genuinely accumulate across applies even though the live state file itself doesn't.
- That "no-op" assumption breaks on an ephemeral runner — Spacelift, Terrateam, etc, and most CI platforms start every run from a fresh checkout, and the mirrored zip file is gitignored, so it never persists between runs. With no file on disk, Terraform can't know its hash until it actually writes one, so it plans (and applies) an update to the Lambda on every single run, re-uploading identical bytes, regardless of whether `github_release_version` actually changed. In practice this only affects `github_release_version` — `zip_path` only takes effect when a real local build is genuinely present, which normally just isn't the case on those same ephemeral runners. We recommend removing `github_release_version` from `lambda_source` (or the whole block) once you're done deploying a given version, rather than leaving it set indefinitely.
- Separately from the above, `github_release_version` also costs a real network fetch on every single `plan`/`apply` where it's set, since Terraform data sources re-fetch on every plan rather than caching — this happens regardless of whether the update-forcing behavior above ends up mattering.
- Expect a `Warning: Response body is not recognized as UTF-8` on every `plan`/`apply` where `lambda_source.github_release_version` is set — this is harmless, not a sign anything's wrong. The `hashicorp/http` provider always populates its plain-string `response_body` attribute internally and warns when it isn't valid UTF-8, regardless of which attribute your config actually reads; a zip file's binary bytes essentially never are. This module only ever reads `response_body_base64` (binary-safe), so the warning doesn't reflect any actual problem with the downloaded content.

The module auto-generates `Description`/`ManagedBy`/`Owner`/`Region` tags (defaulting to an auto-generated sentence, `"IAC"`, `github_username`, and `"us-east-2"` respectively), so a shared/employer account deployment is self-describing without hand-writing ownership prose. That's the whole point of deploying via a visible thin consumer block instead of running this somewhere only you can see: a teammate should be able to find and destroy it without your involvement. Override any of these by setting the same key in the single `tags` variable, e.g. `tags = { Owner = "someone-else", Environment = "Personal" }`. Every taggable resource also gets `Name` and `ResourceType` tags — these two are always accurate to the actual resource and can never be overridden via `tags`, unlike everything else.

`function_name` defaults to `"github-pr-slack-notifier-<github_username>"` so multiple people's instances can coexist in one AWS account without colliding. Every other AWS resource name (Lambda function, IAM role, IAM policy, DynamoDB table) has its own override variable (`lambda_function_name`, `iam_role_name`, `thread_store_policy_name`, `thread_store_table_name`) that defaults to a name derived from `function_name` — dash-case for Lambda/DynamoDB, PascalCase for IAM — so the username flows through into the IAM names too by default. Pass any of them explicitly if you want something other than the default.

After `terraform apply`, take the `function_url` output and set it as the GitHub App's webhook URL.

### Testing changes

To deploy Terraform based on your local repository (e.g. if you're making changes on the repository), you can pull a local source:

```hcl
source = "/path/to/github-pr-slack-notifier/terraform/module"
```

## Kill switch

To stop all notifications immediately, uninstall (or suspend) the GitHub App. There are two independent paths, for two different scenarios:

- **You still have access** (e.g. leaving with notice)
  - Option 1: Suspend or delete the GitHub app yourself from your personal account's App settings (`github.com/settings/apps` → the app → Advanced/Danger Zone). This works regardless of your org membership status, since the App is owned by your personal account, not the org.
  - Option 2: Ask an admin of the GitHub organization to uninstall the GitHub app from the organization (see below)
- **You don't have access** (e.g. offboarded with no notice)
  - Any remaining org admin goes to `github.com/organizations/<org>/settings/installations`, finds the app, and clicks Configure → Uninstall. This requires no access to your accounts at all — it's the path a teammate uses to shut this off without you.

Either path stops webhook delivery immediately. No AWS access is required for either — the Lambda, DynamoDB table, and IAM role are left idle (negligible cost) until someone runs `terraform destroy` on the consumer block at their convenience; the module's auto-generated `Description`/`Owner` tags (see "Deploying") are what let a teammate find and identify it without asking you anything.

## Estimated AWS costs

In practice: **effectively $0/month**, comfortably inside AWS's perpetual free tier for any realistic usage. The math below is meant to be checkable — treat the specific figures as rough (they're standard published `us-east-2` prices at the time of writing; check AWS's current Lambda/DynamoDB/CloudWatch pricing pages for exact numbers).

- **Lambda (requests + duration)** — this is the only resource likely to ever cost anything, and even then, not really. The Lambda runs once per webhook delivery it *receives*, which is every PR comment, review, merge, label change, and title edit across every repo the GitHub App is installed on — not just PRs you're subscribed to, since the subscription/draft check happens *inside* the Lambda, so the invocation itself already happened by the time that check runs. AWS's free tier includes 1,000,000 requests and 400,000 GB-seconds of compute a month, forever (not a 12-month trial). At 128 MB memory (0.125 GB) and a generous 1-second average duration, exhausting the compute allowance alone would take roughly **3.2 million invocations a month** (and the 1M-request limit would bite first, at $0.20/million after that) — several orders of magnitude more PR activity than one team is likely to generate.
- **DynamoDB** (bot-token delivery only, for thread state) — on-demand pricing is roughly $1.25 per million writes and $0.25 per million reads, plus ~$0.25/GB-month storage. Each notification does at most one read and one write of a few dozen bytes, and the 90-day TTL keeps the table tiny. This stays a fraction of a cent per month at any realistic volume.
- **CloudWatch Logs** — the Lambda's execution role writes its own logs (a few short lines per invocation, mostly on errors/rejections). Ingestion is roughly $0.50/GB and storage ~$0.03/GB-month; even thousands of invocations a month produce well under 1 GB. Note: this module doesn't set a log retention policy, so logs accumulate indefinitely by default — the storage cost stays negligible for a long time regardless, but if you want them to expire automatically, add your own `aws_cloudwatch_log_group` with a `retention_in_days` for the function.
- **Lambda Function URL** — no separate charge; it's billed as ordinary Lambda invocations.
- Everything else this module creates (IAM role, IAM policy) has no cost.

## Notes and known limitations

- The module takes secret values directly rather than reading from a specific backend, so it works the same whether your consumer resolves them from SSM, encrypted tfvars, or anywhere else. Whatever you pass in gets set as a plain Lambda environment variable — the Lambda's own IAM role never needs any secrets-backend read access — but those values will also land in Terraform state in plaintext, same as any other resource attribute. Restrict state storage access accordingly.
- The Lambda Function URL has `authorization_type = "NONE"` (GitHub can't do AWS SigV4 auth) — the endpoint is intentionally public, and security is enforced entirely by the webhook HMAC signature check inside the handler.
- `dist/lambda.zip` is gitignored, so it only exists on whichever machine last ran `task build`. Its bytes get mirrored to a fixed internal path (`lambda_deploy.tf`) before `aws_lambda_function` ever reads them, so `dist/lambda.zip`'s own (machine-specific) path never actually reaches that resource and its drift between machines never matters. See "Providing the Lambda's code" above for the full `lambda_source` precedence.
  - Independently of that precedence: `lambda_source.zip_path`'s relative-path resolution is ordinary Terraform filesystem-function behavior — it resolves against the working directory `terraform`/`terragrunt` actually runs in, **not** `path.module` — so a relative value only works if it's correct relative to wherever your pipeline invokes Terraform, which for many CI platforms is the stack's project root, not the repo root.
- Bot-token delivery provisions a small DynamoDB table (pay-per-request, with a 90-day TTL so it doesn't grow unbounded) purely to remember which Slack thread each PR belongs to — this doesn't apply to the "no persistent database" framing elsewhere in this project, which is about subscription determination (still computed live off the GitHub API every time), not notification-thread bookkeeping.
- Single-user only: notifies exactly one GitHub username. See the [design doc](docs/design-pr-subscription-notifier.md)'s Future Improvements section for what multi-user support would require.
- When a PR's title changes and a thread already exists for it (bot-token delivery), the thread's header is updated in place via `chat.update` — this needs no extra GitHub API call, since the new title is already in the `pull_request` webhook payload. If no thread exists yet, or delivery is incoming-webhook, this is a silent no-op. **With incoming-webhook delivery, previously sent messages are never rewritten.** Each flat message reflects whatever the title was at the moment *that* message was sent; renaming a PR does not go back and update earlier messages, it only means the *next* message will show the new title.

---

### Contributing

To submit a feature request, bug ticket, etc, please submit an official [GitHub issue](https://github.com/emmahsax/github-pr-slack-notifier/issues/new). To copy or make changes, please [fork this repository](https://github.com/emmahsax/github-pr-slack-notifier/fork). When/if you'd like to contribute back to this upstream, please create a pull request on this repository.

Please follow included Issue Template(s) and Pull Request Template(s) when creating issues or pull requests.

### Security Policy

To report any security vulnerabilities, please view this repository's [Security Policy](https://github.com/emmahsax/github-pr-slack-notifier/security/policy).

### Licensing

For information on licensing, please see [LICENSE.md](https://github.com/emmahsax/github-pr-slack-notifier/blob/main/LICENSE.md).

### Code of Conduct

When interacting with this repository, please follow [Contributor Covenant's Code of Conduct](https://contributor-covenant.org).

### Releasing

To make a new release:

1. Verify `main` has or will have the newest version in the `main.go` file
1. Merge the pull request via the big green button
3. Trigger a new workflow from [GitHub Actions](https://github.com/emmahsax/github-pr-slack-notifier/actions/workflows/release.yml) and pass in the next sensible semantic version

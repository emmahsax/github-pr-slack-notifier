# GitHub PR → Slack Notifier

A project designed to provide Slack notifications when GitHub PRs change: get pinged in Slack when a pull request you're **"subscribed"** to gets a comment, an approval, a changes-requested review, a merge, or a label added/removed — but only for PRs that are ready for review (not draft), and only when you took an explicit prior action on that PR (authored it, pushed a commit, reviewed it, or commented on it). Being requested for review or @mentioned does *not* count as "subscribed".

Full requirements and design rationale: [`spec/spec-design-pr-subscription-notifier.md`](spec/spec-design-pr-subscription-notifier.md).

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
5. Generate and download a webhook secret and a private key (`.pem`), and take note of the App ID. The Terraform module takes these as direct sensitive input values (`github_app_id`, `github_app_private_key`, `github_webhook_secret`) rather than reading them from any particular backend itself — store them however your Terraform setup already manages secrets (SSM Parameter Store, encrypted tfvars, etc.) and resolve them to values before passing them to the module. If using SSM, I recommend prefixing parameter names with your GitHub username to keep instances distinct if this is ever deployed for more than one person in the same account, e.g. `/github-pr-slack-notifier/<username>/github-app-private-key`.
6. Install the App on whichever GitHub organization(s)/repositories you want notifications from.

## Slack setup

Pick one delivery method:

- **Bot token** (`slack_delivery_method = "bot_token"`), recommended if you want threading:
  1. Create a Slack channel for your PR notifications (e.g. `#emmahsax-prs`).
  2. Create a Slack App with a bot token scoped to `chat:write`, install it to your workspace, and invite the bot user into that channel.
  3. Get the channel's ID (right-click the channel → View channel details, or `#channel-name` in a browser URL bar resolves to a `C...` ID) and set `slack_target` to it.
- **Incoming webhook** (`slack_delivery_method = "incoming_webhook"`): create a Slack incoming webhook for whichever channel you want notifications in. Simpler to set up, but every message is flat (no threading — see "How it works").

Same as the GitHub secrets: pass the resulting token/URL to the module as `slack_credential`, sourced however your Terraform setup manages secrets.

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
  source = "git::https://github.com/emmahsax/github-pr-slack-notifier.git//terraform/module?ref=v0.0.1"

  github_app_id           = "123456"
  github_app_private_key  = data.aws_ssm_parameter.github_app_private_key.value
  github_org_allowlist    = ["my-org"]
  github_username         = "emmahsax"
  github_webhook_secret   = data.aws_ssm_parameter.github_webhook_secret.value

  # Optional — see note below. Omit this entirely if you don't have a local build.
  lambda_zip_path         = "${path.module}/../../dist/lambda.zip"

  slack_credential        = data.aws_ssm_parameter.slack_credential.value
  slack_delivery_method   = "bot_token"
  slack_target            = "C0123456789"
}
```

If your Terraform setup already manages secrets some other way (e.g. encrypted tfvars), skip the data sources and pass those values into the module directly instead.

`lambda_zip_path` is **not required** for most people applying this module — it only matters to whoever is actually pushing a Lambda code update(s). If you leave it unset (or the path it resolves to doesn't exist locally), the module falls back to whatever code is already deployed and leaves the function's `filename` untouched (via `lifecycle.ignore_changes`), so `plan`/`apply` is a clean no-op. This means teammates who just need to `plan`/`apply` other parts of a shared consumer file — without ever running `task build` or downloading a release zip — can do so safely. Only set `lambda_zip_path` when you're the one deploying a real code change (pointing it at `task build`'s output or a downloaded release `lambda.zip`).

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
- `dist/lambda.zip` is gitignored, so it only exists on whichever machine last ran `task build`. When `lambda_zip_path` is missing or unset, the module falls back to whatever code is already deployed for `source_code_hash`, and `filename` itself is excluded from diffing (`lifecycle.ignore_changes`) so its value drifting between machines' local paths never matters — `plan`/`apply` from anyone else is a silent no-op on this resource rather than an error. The one exception: the very first-ever `apply` of a fresh deployment must come from a machine that has actually built/downloaded the zip, since there's no existing function yet to fall back to.
- Bot-token delivery provisions a small DynamoDB table (pay-per-request, with a 90-day TTL so it doesn't grow unbounded) purely to remember which Slack thread each PR belongs to — this doesn't apply to the "no persistent database" framing elsewhere in this project, which is about subscription determination (still computed live off the GitHub API every time), not notification-thread bookkeeping.
- Single-user only (v1): notifies exactly one GitHub username. See the spec's open questions for what multi-user support would require.
- When a PR's title changes and a thread already exists for it (bot-token delivery), the thread's header is updated in place via `chat.update` — this needs no extra GitHub API call, since the new title is already in the `pull_request` webhook payload. If no thread exists yet, or delivery is incoming-webhook, this is a silent no-op. **With incoming-webhook delivery, previously sent messages are never rewritten.** Each flat message reflects whatever the title was at the moment *that* message was sent; renaming a PR does not go back and update earlier messages, it only means the *next* message will show the new title.

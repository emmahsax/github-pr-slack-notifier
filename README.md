# github-pr-slack-notifier

### TODO: update this README as needed

A small application, designed to run as an AWS Lambda, that receives GitHub webhooks about PR updates and can post to Slack

---

### Contributing

To submit a feature request, bug ticket, etc, please submit an official [GitHub issue](https://github.com/emmahsax/github-pr-slack-notifier/issues/new). To copy or make changes, please [fork this repository](https://github.com/emmahsax/github-pr-slack-notifier/fork). When/if you'd like to contribute back to this upstream, please create a pull request on this repository.

Please follow included Issue Template(s) and Pull Request Template(s) when creating issues or pull requests.

### Security Policy

To report any security vulnerabilities, please view this repository's [Security Policy](https://github.com/emmahsax/github-pr-slack-notifier/security/policy).

<!-- Only include the below licensing documentation if the repository contains a license -->

### Licensing

For information on licensing, please see [LICENSE.md](https://github.com/emmahsax/github-pr-slack-notifier/blob/main/LICENSE.md).

### Code of Conduct

When interacting with this repository, please follow [Contributor Covenant's Code of Conduct](https://contributor-covenant.org).

<!-- Only include the below releasing documentation if the repository contains tags and releases -->

### Releasing

<!-- For Ruby gems -->

To make a new release of this gem:

1. Merge the pull request via the big green button
2. Run `git tag vX.X.X` and `git push --tag`
3. Make a new release [here](https://github.com/emmahsax/github-pr-slack-notifier/releases/new)
4. Run `gem build *.gemspec`
5. Run `gem push *.gem` to push the new gem to RubyGems
6. Run `rm *.gem` to clean up your local repository

To set up your local machine to push to RubyGems via the API, see the [RubyGems documentation](https://guides.rubygems.org/publishing/#publishing-to-rubygemsorg).

<!-- For Go binaries -->

To make a new release:

1. Verify `main` has or will have the newest version in the `main.go` file
1. Merge the pull request via the big green button
3. Trigger a new workflow from [GitHub Actions](https://github.com/emmahsax/github-pr-slack-notifier/actions/workflows/release.yml) and pass in the package version indicated in the `main.go` file (but include the `v` prefix)

<!-- Only include the below archival notice if the repository is archived -->

### Archival Notice

This repository has been archived and designated as read-only. From GitHub's documentation:

> This will make the emmahsax/github-pr-slack-notifier repository, issues, pull requests, labels, milestones, projects, wiki, releases, commits, tags, branches, reactions and comments read-only and disable any future comments. The repository can still be forked.

To unarchive this repository at any time, please reach out to me at https://emmasax.com/contact-me/.

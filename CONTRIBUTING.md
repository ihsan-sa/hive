# Contributing

Thanks for looking. The Hive is small on purpose, and most of it is written by its own agents, so here is how outside help fits.

## Ways to help, easiest first
1. **Install it and tell us what broke.** The [Windows and macOS paths](docs/install.md) and the list of [what isn't tested yet](https://hivelab.ca/guide/guide.html#not-tested-yet) need real machines. A bug report from a clean install is worth more than a patch.
2. **Show what you made.** Post it in [Discussions](https://github.com/ihsan-sa/hive/discussions) under Show and tell. Examples with a real first message and a real result may be added to [`examples/`](examples/README.md).
3. **Fix or improve the docs.** Anything in `docs/` or the README that cost you time.
4. **Take a [good first issue](https://github.com/ihsan-sa/hive/labels/good%20first%20issue).**
5. **Write an extra** (a module): a folder with a `module.json`. See "Extras" in the README and `modules/extras/README.md`. Extras can live in your own repo and be installed with `hive module add <git URL>`.

## Sending a change
- Open an issue first for anything bigger than a typo, so you don't build something we can't take.
- This repository is a published copy: each release is built from the Hive's working source and tagged. We read pull requests here and, if we take one, apply it to the source, so it arrives in the next release with your name on the commit. That is slower than a normal merge, and we say so here rather than leave your pull request sitting.
- Keep one change per pull request, and say what you ran to check it. Tests are in the source and are not part of this copy, so describe what you did by hand: the install on which system, the page you clicked, the command and its output.
- Wording matters here. The README and guide use the words the app shows (**Chat**, **Settings**, "conversation", "extras"). Keep a statement in one place and link to it.
- Don't add telemetry, a new default network call, or anything that gives agents more rights without saying so in the pull request. Those get the closest look.

## Reporting a security problem
Not in an issue. See [SECURITY.md](SECURITY.md).

## Conduct
See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## Licence
By contributing you agree that your change is under the [MIT licence](LICENSE).

## Sign-off (DCO)
Sign off each commit with `git commit -s`. That adds a `Signed-off-by:` line and says you have the right to submit the change under this licence, as set out in the [Developer Certificate of Origin](https://developercertificate.org/).

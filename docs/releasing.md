# Releasing nine-tails

Tagged releases are built by GoReleaser for macOS, Linux, and Windows on
`amd64` and `arm64`. The macOS binaries are Developer ID signed and notarized
before they enter the archives. A successful stable release also updates the
`nine-tails` cask in `scottmeyer/homebrew-tap`.

The release configuration deliberately keeps snapshots secret-free:

```sh
goreleaser check
goreleaser release --snapshot --clean --skip=publish
./dist/nine-tails_darwin_arm64*/nine-tails --version
```

Use the Go version in `go.mod` and GoReleaser v2.18.0 when reproducing CI
artifacts. Release builds use `CGO_ENABLED=0`, `-trimpath`, the source commit's
timestamp, and an explicit version linker flag.

## One-time GitHub setup

Create a public repository named `scottmeyer/homebrew-tap` with a `main`
branch. The `homebrew-` prefix is significant: Homebrew maps the short tap
name `scottmeyer/tap` to that repository. GoReleaser writes
`Casks/nine-tails.rb` there; do not maintain a second copy in this repository.

Create a fine-grained personal access token scoped only to
`scottmeyer/homebrew-tap`, with repository **Contents: Read and write**, and
store it on `scottmeyer/nine-tails` as:

- `HOMEBREW_TAP_GITHUB_TOKEN`

The normal workflow `GITHUB_TOKEN` publishes the GitHub Release in this
repository. It cannot update a different repository, which is why the tap
token is separate.

The tag workflow needs these permissions:

```yaml
permissions:
  contents: write
  id-token: write
  attestations: write
```

`contents: write` is required by GoReleaser. The other two permissions are
needed only when the workflow creates GitHub artifact attestations.

## Apple signing and notarization

Use a **Developer ID Application** certificate, not a development,
Mac App Distribution, or Developer ID Installer certificate. Install the
certificate and its private key in Keychain Access, then export both as a
password-protected PKCS #12 (`.p12`) file.

Create an App Store Connect API key for the same developer team and retain its
`.p8` file, key ID, and issuer ID. Encode the two files without line wrapping:

```sh
openssl base64 -A -in Certificates.p12
openssl base64 -A -in AuthKey_ABC123XYZ.p8
```

Store the values as GitHub Actions secrets:

- `MACOS_SIGN_P12`: base64 output for the `.p12` file
- `MACOS_SIGN_PASSWORD`: password used when exporting the `.p12`
- `MACOS_NOTARY_KEY`: base64 output for the `.p8` file
- `MACOS_NOTARY_KEY_ID`: App Store Connect key ID
- `MACOS_NOTARY_ISSUER_ID`: App Store Connect issuer UUID

GoReleaser OSS can sign and notarize bare macOS binaries on a Linux runner by
using the Notary API; no GoReleaser Pro license or macOS keychain setup is
required. The configuration waits for Apple's verdict for at most 20 minutes,
so the workflow's GoReleaser timeout must be at least 30 minutes.

The release workflow must fail before GoReleaser when any of the five Apple
secrets or `HOMEBREW_TAP_GITHUB_TOKEN` is empty. Conditional notarization is
present only so `goreleaser check` and snapshot builds work without secrets;
it is not permission to publish unsigned stable artifacts. Do not add an
`xattr -d com.apple.quarantine` Homebrew hook: it bypasses the Gatekeeper check
that signing and notarization are intended to provide.

## Tag workflow contract

The release workflow should run only for tags matching `v*.*.*`, fetch the
complete Git history and tags, set up the Go version from `go.mod`, perform the
secret preflight above, and then run:

```sh
goreleaser release --clean --timeout 30m
```

Pin the GoReleaser action itself by commit SHA and request GoReleaser
`v2.18.0`. Pass the standard `GITHUB_TOKEN`, the tap token, and all five Apple
secrets as environment variables with the exact names listed above. Keep the
ordinary CI test job separate and require it before tags are cut.

## Cutting a release

From a clean, current `main` after CI succeeds:

```sh
version=v0.1.0
git tag -a "$version" -m "nine-tails $version"
git push origin "$version"
```

Use a `v`-prefixed semantic version. GoReleaser strips the prefix for
`nine-tails --version`, creates archives and `checksums.txt`, waits for Apple
notarization, publishes the GitHub Release, and finally updates the tap. A
prerelease tag such as `v0.2.0-rc.1` creates a GitHub prerelease but does not
replace the stable Homebrew cask.

## Verifying a published release

Check both macOS architectures at least once before announcing the first
release. On a Mac matching the downloaded archive:

```sh
shasum -a 256 -c checksums.txt --ignore-missing
codesign --verify --deep --strict --verbose=2 nine-tails
spctl --assess --type execute --verbose=4 nine-tails
./nine-tails --version
```

A bare executable cannot carry a stapled ticket, so `spctl` may need network
access to retrieve its notarization ticket from Apple on first assessment.

Finally test the public distribution path:

```sh
brew install --cask scottmeyer/tap/nine-tails
nine-tails --version
brew audit --cask --strict scottmeyer/tap/nine-tails
```

The fully qualified install trusts only this cask rather than every item that
might later appear in the tap.

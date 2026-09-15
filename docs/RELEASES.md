# Automatic releases (GitHub Actions)

Every new commit on `master` (and `main`) creates a **new GitHub Release**
with Windows and macOS binaries plus a source ZIP.

The workflow lives in [`.github/workflows/release.yml`](../.github/workflows/release.yml)
and is heavily commented — this document is about *how to use it* and
*what to set in the repository*, not a line-by-line YAML walkthrough.

Application name on the release page: **Lukas-Anti-Censorship-Proxy**.
File names use the short name **lacp**.

## What lands in a release

| Asset | Contents |
| --- | --- |
| `lacp-windows-amd64.exe` | Windows x86-64 |
| `lacp-windows-arm64.exe` | Windows ARM64 |
| `lacp-darwin-amd64` | macOS Intel |
| `lacp-darwin-arm64` | macOS Apple Silicon |
| `lacp-source.zip` | Source of that commit (`git archive`) |
| `SHA256SUMS.txt` | SHA-256 of all of the above |

File names **do not contain the SHA**. That is why the stable URL:

```
https://github.com/<user>/<repo>/releases/latest/download/lacp-windows-amd64.exe
```

always downloads the newest release from `master`/`main`.

The release tag **does** contain the short SHA, e.g. `master-a1b2c3d`.
Each commit = its own tag = its own Releases page.

Linux **does not** get a pre-built binary. The ZIP has the sources and
[BUILD.md](BUILD.md).

## First time you run the pipeline

### 1. Repository on GitHub

Locally (in the project directory):

```bash
cd /path/to/Lukas-Anti-Censorship-Proxy    # project directory
git init -b master
git add .
git commit -m "Initial commit: Lukas-Anti-Censorship-Proxy (lacp)"
```

On GitHub: *New repository* (empty, no README — we have our own). Then:

```bash
git remote add origin https://github.com/<your-user>/Lukas-Anti-Censorship-Proxy.git
git push -u origin master
```

If GitHub created the repo with `main`, or `git init` ran without `-b master`,
the workflow still runs: it listens on **both** names.

### 2. Actions permissions

`GITHUB_TOKEN` must be allowed to create releases.

1. Repo → **Settings** → **Actions** → **General**.
2. Bottom: **Workflow permissions**.
3. Choose **Read and write permissions**.
4. Save.

The YAML also has `permissions: contents: write`, which is enough in most
new repositories. Step 3 is a safety net on repos with a stricter policy.

### 3. What happens after `git push origin master`

1. The **Actions** tab shows a job “Release (Windows, macOS, source)”.
2. A green check in about 1–2 minutes (tests + 4 cross-compiles).
3. The **Releases** tab shows `Lukas-Anti-Censorship-Proxy master-<sha>`.

Every later commit on `master`/`main` repeats this cycle and marks the new
release as **Latest**.

## Manual run

Actions → *Release (Windows, macOS, source)* → **Run workflow**.

Rebuilds the **current** tip of the branch you run it on. If the tag
`master-<sha>` already exists (re-run of the same commit), the job
**replaces** files in the existing release instead of failing.

## Version baked into the binary

At startup the proxy prints version and SHA, for example:

```
Lukas-Anti-Censorship-Proxy (lacp) master-a1b2c3d (commit a1b2c3d4e5f6…)
listening on 127.0.0.1:45777
```

The values are stamped by the linker (`-X main.version=…` / `-X main.commit=…`).
A local `go build` without those flags leaves `dev` / `unknown`.

## Verifying SHA-256

Linux / macOS:

```bash
sha256sum -c SHA256SUMS.txt
```

Windows (PowerShell), one file:

```powershell
Get-FileHash .\lacp-windows-amd64.exe -Algorithm SHA256
```

Compare with the line in `SHA256SUMS.txt`.

## Job step order

```
checkout → setup-go → go test → tag/version
        → build win/mac → git archive (ZIP)
        → sha256sum → notes → gh release create
```

If `go test` returns non-zero, there are **no** binaries and **no** release.
The fix is a new commit (or a re-run after a fix on the same branch).

## What the workflow deliberately does not do

- **Does not notarize** Apple binaries / **does not** Authenticode-sign.
  Gatekeeper and SmartScreen will complain — normal for free CI.
- **Does not publish** a Docker image or `deb`/`rpm` packages.
- **Does not build** a Linux ELF (sources are in the ZIP).
- **Does not delete** old releases. Commit history = release history.
  If that fills Releases, delete old ones by hand or add a retention job
  (intentionally omitted — every commit is supposed to be a release).

## Debugging

| Symptom | What to check |
| --- | --- |
| Workflow never appears | Push went to a branch other than `master`/`main`. The YAML file must live in `.github/workflows/`. |
| Job red on “Checkout” | Empty repo, bad ref. |
| Job red on “Run tests” | Run `go test -v ./...` locally — same code. |
| Job red on “Create or update GitHub Release” | Missing `contents: write`. See Settings → Actions. Message `Resource not accessible by integration` is almost always that. |
| Release exists but has no files | The `gh release create` step got an empty `dist/`. Check the compile-step log. |
| Two jobs at once | `concurrency.group` queues per branch; they wait, they do not vanish. |

Full log: repo → **Actions** → the run → the step with the error icon.

## Changing behaviour (common edits)

Everything is in `release.yml`, in commented blocks.

- **Only `master`, no `main`:** remove `- main` from `on.push.branches`.
- **Add Linux amd64:** add `build linux amd64 lacp-linux-amd64` and include
  the file in the `assets` list.
- **Do not mark every release as Latest:** drop `--latest`.
- **Tag `v1.2.3` instead of `master-<sha>`:** change the scheme in
  “Compute tag…” (remember: with *every* commit the tag must be unique).

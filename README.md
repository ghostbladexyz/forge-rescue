# forge-rescue

`forge-rescue` is a small CLI for evacuating repositories from a Gitea instance before age-based deletion.

It does two things:

- `scan` lists accessible user and organization repositories and classifies age-based deletion risk.
- `rescue` mirror-clones selected repositories and exports raw Gitea metadata.
- `upload github` bulk-creates private GitHub repositories and pushes rescued mirrors.
- `delete github` deletes explicitly named GitHub repositories.

It does not migrate issues, recreate pull requests, or synchronize repositories.

## Install

PowerShell:

```powershell
go install github.com/ghostbladexyz/forge-rescue@latest
```

Bash:

```bash
go install github.com/ghostbladexyz/forge-rescue@latest
```

From this checkout:

PowerShell:

```powershell
go build .
```

Bash:

```bash
go build .
```

## Usage

Create a Gitea personal access token with repository read access, then expose it for the current shell:

PowerShell:

```powershell
$env:FORGE_RESCUE_TOKEN = "your-token"
```

Bash:

```bash
export FORGE_RESCUE_TOKEN="your-token"
```

To create the Gitea token:

1. Open your Gitea instance in the browser.
2. Go to your profile menu, then `Settings`.
3. Open `Applications`.
4. Create a new personal access token.
5. Give it read access to repositories, users, and organizations.
6. Copy the token immediately and set it as `FORGE_RESCUE_TOKEN`.

Scan an instance:

PowerShell, from this checkout:

```powershell
.\forge-rescue.exe scan --instance https://platform.zone01.gr/git
```

Bash, from this checkout:

```bash
./forge-rescue scan --instance https://platform.zone01.gr/git
```

This writes:

```text
forge-rescue-data/
  scan.json
```

Rescue high-risk repositories from the last scan:

PowerShell:

```powershell
.\forge-rescue.exe rescue --high-risk
```

Bash:

```bash
./forge-rescue rescue --high-risk
```

Rescue medium-risk repositories from the last scan:

PowerShell:

```powershell
.\forge-rescue.exe rescue --medium-risk
```

Bash:

```bash
./forge-rescue rescue --medium-risk
```

Rescue specific repositories:

PowerShell:

```powershell
.\forge-rescue.exe rescue owner/repo another-owner/another-repo
```

Bash:

```bash
./forge-rescue rescue owner/repo another-owner/another-repo
```

Upload rescued mirrors to GitHub:

PowerShell:

```powershell
$env:GITHUB_TOKEN = "your-github-token"
.\forge-rescue.exe upload github --owner your-github-username
```

Bash:

```bash
export GITHUB_TOKEN="your-github-token"
./forge-rescue upload github --owner your-github-username
```

To create the GitHub token:

1. Open GitHub in the browser.
2. Go to `Settings`.
3. Open `Developer settings`.
4. Open `Personal access tokens`.
5. Open `Tokens (classic)`.
6. Create a new classic token.
7. Select both scopes:
   - `repo`
   - `delete_repo`
8. Copy the token immediately and set it as `GITHUB_TOKEN`.

Delete this GitHub token after you finish rescuing, uploading, and deleting repositories. A classic token with `repo` and `delete_repo` can create, modify, and delete repositories in your account.

GitHub repositories are created as private by default. A rescued Gitea repository named `owner/repo` is uploaded to a GitHub repository named `owner-repo`.

Delete GitHub repositories by explicit name:

PowerShell:

```powershell
.\forge-rescue.exe delete github --owner your-github-username --delete-repo owner-repo another-owner-another-repo
```

Bash:

```bash
./forge-rescue delete github --owner your-github-username --delete-repo owner-repo another-owner-another-repo
```

You can also pass original Gitea-style names. They are mapped the same way as upload:

PowerShell:

```powershell
.\forge-rescue.exe delete github --owner your-github-username --delete-repo owner/repo another-owner/another-repo
```

Bash:

```bash
./forge-rescue delete github --owner your-github-username --delete-repo owner/repo another-owner/another-repo
```

Example:

```text
Gitea repo:  lpapanthy/go-reloaded
GitHub repo: lpapanthy-go-reloaded
```

These two commands delete the same GitHub repository:

PowerShell:

```powershell
.\forge-rescue.exe delete github --owner ghostbladexyz --delete-repo lpapanthy-go-reloaded
```

```powershell
.\forge-rescue.exe delete github --owner ghostbladexyz --delete-repo lpapanthy/go-reloaded
```

Bash:

```bash
./forge-rescue delete github --owner ghostbladexyz --delete-repo lpapanthy-go-reloaded
```

```bash
./forge-rescue delete github --owner ghostbladexyz --delete-repo lpapanthy/go-reloaded
```

Both delete:

```text
https://github.com/ghostbladexyz/lpapanthy-go-reloaded
```

For deleting GitHub repositories, use a token that can delete repositories:

- Classic token: `delete_repo` scope.

Output is written to:

```text
forge-rescue-data/
  repos/
    owner-repo.git/
  metadata/
    owner-repo/
      repo.json
      issues.json
      releases.json
      labels.json
  manifest.json
  upload-github.json
```

## Risk Rules

Default repository age thresholds:

- `HIGH`: created more than 365 days ago
- `MEDIUM`: created more than 180 days ago
- `SAFE`: anything newer

An active repository can still be high risk if it was created more than a year ago. If a repository has no creation timestamp in the API response, `updated_at` is used as a fallback.

## Notes

`rescue` shells out to the real `git` binary and runs `git clone --mirror`. For private repositories, your local Git credential setup must be able to clone from the Gitea instance.

`upload github` shells out to `git push --mirror`. If a GitHub repository already exists and has refs, it is skipped by default to avoid overwriting or deleting existing branches and tags. Use `--force-existing` only when you intentionally want the local mirror to replace the GitHub refs.

`delete github --delete-repo` permanently deletes the named GitHub repositories from the selected `--owner`. Only pass names you have already verified.

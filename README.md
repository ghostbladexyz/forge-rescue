# forge-rescue

[![CI](https://github.com/ghostbladexyz/forge-rescue/actions/workflows/ci.yml/badge.svg)](https://github.com/ghostbladexyz/forge-rescue/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/ghostbladexyz/forge-rescue)](https://github.com/ghostbladexyz/forge-rescue/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/ghostbladexyz/forge-rescue?logo=go)](go.mod)

`forge-rescue` is a small CLI for evacuating repositories from a Gitea instance before *age-based deletion*.

**It does four things:**

- `scan` lists accessible user and organization repositories and classifies age-based deletion risk.
- `rescue` mirror-clones selected repositories and exports raw Gitea metadata.
- `upload github` bulk-creates private GitHub repositories and pushes rescued mirrors.
- `delete github` deletes explicitly named GitHub repositories.

**Out of scope:** it does not migrate issues, recreate pull requests, or synchronize repositories.

## Requirements

- Go 1.22 or newer.
- Git installed and available in your shell `PATH`.
- A Gitea personal access token for scanning and rescuing.
- A GitHub classic token for uploading or deleting GitHub repositories.

## Install

### From Go

**PowerShell**

```powershell
go install github.com/ghostbladexyz/forge-rescue@latest
```

**Bash**

```bash
go install github.com/ghostbladexyz/forge-rescue@latest
```

### From This Checkout

**PowerShell**

```powershell
go build .
```

**Bash**

```bash
go build .
```

## Usage

### Gitea Token

Create a Gitea personal access token with repository read access, then expose it for the current shell:

**PowerShell**

```powershell
$env:FORGE_RESCUE_TOKEN = "your-token"
```

**Bash**

```bash
export FORGE_RESCUE_TOKEN="your-token"
```

**To create the Gitea token:**

1. Open your Gitea instance in the browser.
2. Go to your profile menu, then `Settings`.
3. Open `Applications`.
4. Create a new personal access token.
5. Give it read access to repositories, users, and organizations.
6. Copy the token immediately and set it as `FORGE_RESCUE_TOKEN`.

### Scan

**PowerShell, from this checkout**

```powershell
.\forge-rescue.exe scan --instance https://platform.zone01.gr/git
```

**Bash, from this checkout**

```bash
./forge-rescue scan --instance https://platform.zone01.gr/git
```

This initializes the rescue workspace and writes the repository scan:

```text
forge-rescue-data/
  workspace.json
  scan.json
```

### Rescue

**High-risk repositories**

PowerShell:

```powershell
.\forge-rescue.exe rescue --high-risk
```

Bash:

```bash
./forge-rescue rescue --high-risk
```

**Medium-risk repositories**

PowerShell:

```powershell
.\forge-rescue.exe rescue --medium-risk
```

Bash:

```bash
./forge-rescue rescue --medium-risk
```

**Specific repositories**

PowerShell:

```powershell
.\forge-rescue.exe rescue owner/repo another-owner/another-repo
```

Bash:

```bash
./forge-rescue rescue owner/repo another-owner/another-repo
```

### GitHub Upload

**PowerShell**

```powershell
$env:GITHUB_TOKEN = "your-github-token"
.\forge-rescue.exe upload github --owner your-github-username
```

**Bash**

```bash
export GITHUB_TOKEN="your-github-token"
./forge-rescue upload github --owner your-github-username
```

**To create the GitHub token:**

1. Open GitHub in the browser.
2. Go to `Settings`.
3. Open `Developer settings`.
4. Open `Personal access tokens`.
5. Open `Tokens (classic)`.
6. Create a new classic token.
7. Select the required scopes:
   - `repo`
   - `delete_repo`
   - `read:org` when uploading to or deleting from an organization
8. Copy the token immediately and set it as `GITHUB_TOKEN`.

**Delete this GitHub token after you finish** rescuing, uploading, and deleting repositories. A classic token with `repo` and `delete_repo` can create, modify, and delete repositories in your account; `read:org` lets forge-rescue validate an organization before making any changes.

GitHub repositories are created as private by default. A rescued Gitea repository named `owner/repo` is uploaded to a GitHub repository named `owner-repo`.

`--owner` may name the authenticated GitHub user or an organization that user can create repositories in. forge-rescue resolves the owner automatically and uses the correct GitHub creation endpoint.

Destination names are persisted in `workspace.json` before upload. If two source names both flatten to the same `owner-repository` name, forge-rescue adds the stable source repository ID, such as `owner-repository-123`, instead of guessing or overwriting.

### GitHub Delete

**Warning:** deletion is permanent from GitHub's side. Make sure the repositories exist locally in `forge-rescue-data/repos/` or somewhere else before deleting anything.

Deletion accepts exact GitHub repository names only. Repeat the owner with `--confirm-delete` so a typo in the selected account cannot start a destructive batch.

**Delete by exact GitHub repository name**

PowerShell:

```powershell
.\forge-rescue.exe delete github --owner your-github-username --confirm-delete your-github-username owner-repo another-owner-another-repo
```

Bash:

```bash
./forge-rescue delete github --owner your-github-username --confirm-delete your-github-username owner-repo another-owner-another-repo
```

Original Gitea names such as `owner/repo` are rejected here because deletion never performs implicit name conversion. Copy the exact destination name from `workspace.json`, `upload-github.json`, or GitHub.

For deleting GitHub repositories, use a token that can delete repositories:

- Classic token: `delete_repo` scope.

### Output

```text
forge-rescue-data/
  workspace.json
  scan.json
  repos/
    repo-123.git/
  metadata/
    repo-123/
      repo.json
      issues.json
      releases.json
      labels.json
  manifest.json
  upload-github.json
```

`workspace.json` keeps the source repository identity separate from local artifact names and GitHub destination names. New artifacts use the stable Gitea repository ID when available. Existing workspaces that use flattened `owner-repo` names remain readable and are not moved automatically; forge-rescue stops with an error if a flattened legacy name could refer to more than one repository.

## Risk Rules

**Default repository age thresholds:**

- `HIGH`: created more than 365 days ago
- `MEDIUM`: created more than 180 days ago
- `SAFE`: anything newer

An active repository can still be **high risk** if it was created more than a year ago. If a repository has no creation timestamp in the API response, `updated_at` is used as a fallback.

## Notes

**`rescue`:** shells out to the real `git` binary and runs `git clone --mirror`. Clones are validated as bare repositories before they are moved into their final workspace path, so an interrupted clone is cleaned up instead of being treated as rescued. Repository metadata is fetched completely before a two-rename directory swap begins. If the process stops between those renames, the next workspace open or read restores the previous complete capture; once the new capture reaches its canonical path, failure to remove the obsolete backup does not change the completed outcome. For private repositories, your local Git credential setup must be able to clone from the Gitea instance.

**`upload github`:** validates each local mirror before shelling out to `git push --mirror`. The GitHub token is supplied through a temporary non-interactive credential environment; it is not placed in the remote URL or Git process arguments. If a GitHub repository already exists and has refs, it is skipped by default to avoid overwriting or deleting existing branches and tags. Use `--replace-existing-refs` only when you intentionally want the local mirror to replace every GitHub ref. The older `--force-existing` spelling remains as a deprecated alias for one release.

**`delete github --confirm-delete OWNER`:** permanently deletes the exact named GitHub repositories from the selected `--owner`. A missing repository is reported as skipped because GitHub uses the same response when a private repository is inaccessible.

## Common Errors

**`forge-rescue` is not recognized:**
Use `.\forge-rescue.exe` in PowerShell or `./forge-rescue` in Bash when running from this checkout.

**`403 Forbidden` from GitHub:**
Your token is missing the required `repo` or `delete_repo` scope, or the authenticated user cannot manage repositories for `--owner`.

**`local mirror missing` during upload:**
Run `rescue` before `upload`. Upload only pushes repositories that exist in `forge-rescue-data/repos/`.

## Development

```bash
go test ./...
go vet ./...
go build .
```

CI runs these checks, formatting verification, and race-enabled tests with both Go 1.22 and the current stable Go release.

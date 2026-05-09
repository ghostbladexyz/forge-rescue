# forge-rescue

`forge-rescue` is a small CLI for evacuating repositories from a Gitea instance before age-based deletion.

It does two things:

- `scan` lists accessible user and organization repositories and classifies age-based deletion risk.
- `rescue` mirror-clones selected repositories and exports raw Gitea metadata.

It does not migrate issues, recreate pull requests, upload to another forge, or synchronize repositories.

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
  scan.json
```

## Risk Rules

Default repository age thresholds:

- `HIGH`: created more than 365 days ago
- `MEDIUM`: created more than 180 days ago
- `SAFE`: anything newer

An active repository can still be high risk if it was created more than a year ago. If a repository has no creation timestamp in the API response, `updated_at` is used as a fallback.

## Notes

`rescue` shells out to the real `git` binary and runs `git clone --mirror`. For private repositories, your local Git credential setup must be able to clone from the Gitea instance.

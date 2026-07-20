# Repository Rescue

Repository Rescue preserves repositories and their archival metadata before a source forge removes them.

## Language

**Source repository identity**:
The stable identity assigned to a repository by its source forge. A repository's human-readable owner and name describe its address, not its identity.
_Avoid_: Repository name, flattened name

**Gitea source**:
The authenticated Gitea instance that supplies the deterministic repository catalog and archival metadata captured during rescue.
_Avoid_: Gitea service, API wrapper

**Repository metadata capture**:
One complete in-memory archive of a source repository record, issues, releases, and labels; it is persisted only after every required remote request succeeds.
_Avoid_: Metadata export, partial metadata

**Rescue workspace**:
The durable collection of scans, rescue artifacts, and run outcomes associated with one source forge.
_Avoid_: Data directory, output folder

**Scan**:
A point-in-time inventory of repositories visible to the authenticated source-forge user.
_Avoid_: Repository list

**Rescue artifact**:
The Git mirror and archival metadata captured for one source repository identity.
_Avoid_: Backup folder, downloaded repository

**Rescue manifest**:
The durable outcome of a rescue run, including every selected source repository and its result.
_Avoid_: Status file, log

**GitHub destination**:
The authenticated GitHub user or organization that receives rescued repositories.
_Avoid_: Upload target, GitHub client

**Destination repository name**:
The durable, exact GitHub name assigned to one source repository identity independently from its rescue artifact key.
_Avoid_: Flattened name, artifact name

**Legacy workspace**:
A rescue workspace created by an earlier forge-rescue release. It remains readable and is never reorganized automatically.
_Avoid_: Old data folder

**Risk level**:
The age-based classification derived from a repository's creation time, or its last update time when creation time is unavailable.
_Avoid_: Activity level, inactivity status

# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased](https://github.com/ghostbladexyz/forge-rescue/compare/v0.3.0...HEAD)

### Added

- Add a versioned rescue workspace with durable source identities, destination allocations, and resumable outcomes.
- Add deterministic Gitea discovery, complete metadata capture, and structured GitHub upload and deletion reports.
- Add GitHub Actions checks for Go 1.22 and the current stable Go release.

### Changed

- Replace implicit GitHub deletion-name conversion with exact names and explicit owner confirmation.
- Replace `--force-existing` with the clearer `--replace-existing-refs` flag while retaining a deprecated compatibility alias.

### Fixed

- Prevent local artifact and GitHub destination collisions between repositories with similar flattened names.
- Correct GitHub repository creation for organization owners.
- Restore the documented Go 1.22 test and vet compatibility.

### Security

- Keep GitHub credentials out of Git URLs, process arguments, reports, and command errors.
- Validate Git mirrors and use recoverable metadata directory swaps that preserve a complete capture across interruption and cleanup failure.

### Documentation

- Add the repository-rescue domain glossary and document workspace compatibility and safety behavior.

## [0.3.0](https://github.com/ghostbladexyz/forge-rescue/compare/v0.2.0...v0.3.0) (2026-05-10)

### Added

- *(internal)* add GitHub repository deletion ([#3](https://github.com/ghostbladexyz/forge-rescue/commit/de0399372cbe824b8a59d2e8a3c30157bd471859))

## [0.2.0](https://github.com/ghostbladexyz/forge-rescue/compare/v0.1.0...v0.2.0) (2026-05-10)

### Added

- *(internal)* add GitHub bulk upload ([#2](https://github.com/ghostbladexyz/forge-rescue/commit/bf581eb887be1a89ba0ad79a06fc986e75593ac3))

## 0.1.0 (2026-05-09)

### Added

- *(internal)* add Gitea rescue workflow ([#1](https://github.com/ghostbladexyz/forge-rescue/commit/71206ed0d9e0132ae5685d91db71f54de6b1a56d))

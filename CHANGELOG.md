# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## 1.0.6 - 2024-10-02

### Added

- Added retry mechanism for when the unix socket is not yet available

## 1.0.5 - 2024-10-02

### Changed

- Use a custom build of `socat` instead buggy local

## 1.0.4 - 2024-09-30

### Changed

- Graceful close of the native windows relay
- Optional `--pid-file` argument, pass it only if not empty

## 1.0.3 - 2024-09-30

### Changed

- Use signal to termination due to parent process not being present anymore

## 1.0.2 - 2024-09-30

### Fixed

- The `container-desktop-wsl-relay.exe` is now able to close itself

## 1.0.1 - 2024-09-30

### Added

- Initial release

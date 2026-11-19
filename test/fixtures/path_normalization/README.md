# Path Normalization Test Fixtures

This directory contains test fixtures for path normalization across different build environments.

## Fixture Types

### 1. Unix-style paths
- `/home/user/project/src/main.rs`
- `/build/workspace/crates/token/src/lib.rs`
- Relative paths: `src/lib.rs`, `../utils/helpers.rs`

### 2. Windows-style paths
- `C:\Users\user\project\src\main.rs`
- `D:\build\workspace\crates\token\src\lib.rs`
- Relative paths: `src\lib.rs`, `..\utils\helpers.rs`

### 3. Mixed separator paths
- `C:/build/workspace/src/main.rs`
- `/home/user\project\src/lib.rs` (unusual but possible)

### 4. Container paths
- `/workspace/src/main.rs` (Docker/container environments)
- `/app/crates/token/src/lib.rs`

### 5. CI/CD paths
- `/home/runner/work/project/project/src/main.rs` (GitHub Actions)
- `/builds/username/project/src/main.rs` (GitLab CI)

## Usage

These fixtures are used in tests to verify that the PathNormalizer correctly handles:
- Separator normalization (Windows vs Unix)
- Workspace root resolution
- Explicit remapping tables
- Case sensitivity
- Directory traversal safety
- Null byte detection

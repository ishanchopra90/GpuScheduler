# refresh_profiles

Utility script for hardware profile maintenance.

## Commands

- Regenerate simulator JSON from YAML source-of-truth:
  - `make refresh-profiles`
- Check that embedded JSON is up to date:
  - `make refresh-profiles-check`
- Fetch official source pages and build source snapshot artifacts:
  - `make refresh-profiles-snapshot`

## Snapshot artifacts

Running `make refresh-profiles-snapshot` produces:

- `tools/refresh_profiles/hardware_sources_snapshot.json`
  - Per-profile list of source URLs with status code, content type, bytes, SHA256, title, and saved local file path.
- `tools/refresh_profiles/sources/`
  - Raw fetched source pages (HTML/PDF bytes).
- `configs/hardware_profiles.review.yaml`
  - Proposed YAML output for manual review only.

The script intentionally does not overwrite `configs/hardware_profiles.yaml` during snapshot mode.
Review `configs/hardware_profiles.review.yaml`, then copy approved changes into the canonical YAML manually.

## What

<!-- Ringkas perubahan dalam 1-2 kalimat -->

## Why

<!-- Konteks bisnis / link ke ticket -->
Closes #

## How

<!-- High-level approach. Untuk perubahan signifikan, tambahkan ADR -->

## Risk Assessment

- [ ] No DB migration (atau aman backward-compatible)
- [ ] No breaking change ke API
- [ ] No secret/key change
- [ ] Test coverage ≥ existing
- [ ] Logged & traceable

## Testing

- [ ] Unit tests pass
- [ ] Integration tests pass (kalau berkaitan dengan adapter)
- [ ] Manual test di dev cluster
- [ ] Load test (kalau touch hot path)

## Rollback Plan

<!-- Cara revert kalau ada masalah di prod -->

## Screenshots / Output

<!-- Optional: untuk perubahan UI/API yang user-facing -->

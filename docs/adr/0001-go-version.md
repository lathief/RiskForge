# ADR 0001: Versi Go 1.25 untuk baseline

- Status: Superseded by [ADR 0029](0029-go-1-26-and-module-path.md) (25 November 2026, Go 1.26)
- Tanggal: 2026-11-17

## Konteks

Versi awal `SYSTEM_DESIGN.md` menargetkan Go 1.27.x (dokumen kini memakai Go 1.25.x dan menyebut 1.27.x sebagai target awal). Scaffold resmi Goravel v1.18.1 mensyaratkan `go 1.25.0` di `go.mod`, dan image `golang:1.27-bookworm` belum tersedia saat RF-001 dikerjakan.

## Keputusan

Gunakan Go 1.25 (`golang:1.25-bookworm`) sebagai baseline. Versi dipin lewat `go.mod` dan default `GO_IMAGE` di `compose.yaml`.

## Konsekuensi

- Kompatibilitas dengan Goravel v1.18.1, `goravel/fiber` v1.18.1, dan `goravel/postgres` v1.18.0 sudah terverifikasi lewat build dan test.
- Naik ke Go 1.27 dilakukan dengan mengubah `go.mod` dan `GO_IMAGE`, lalu menjalankan ulang seluruh test.

# ADR 0029: Go 1.26, path modul `github.com/lathief/RiskForge`, dan OpenTelemetry 1.46

- Status: Accepted
- Tanggal: 2026-11-25
- Tiket: validasi Milestone 5, BACKLOG SEC-11 (High); menggantikan ADR 0001

## Konteks

`govulncheck` menemukan 17 kerentanan yang dapat dijangkau (SEC-11, High): 13 di pustaka standar Go dan `golang.org/x/net` yang perbaikannya hanya ada di Go 1.26.x, dan 4 di OpenTelemetry. ADR 0001 memakai Go 1.25 karena scaffold Goravel v1.18.1 mensyaratkannya. Go 1.26.11 kini tersedia (`golang:1.26-bookworm`, `golang:1.26-alpine`), dan Goravel v1.18.1 tetap rilis terbaru.

Path modul masih `goravel`, nama bawaan scaffold. Nama itu bukan path yang dapat di-import dari luar, tidak menunjukkan repositori, dan muncul di setiap import paket aplikasi.

## Keputusan

1. **Go 1.26.** `go.mod` memakai `go 1.26.11` (versi pertama yang memuat semua perbaikan pustaka standar yang dilaporkan, termasuk GO-2026-6859), image dev dan CI `golang:1.26-bookworm`, dan builder image rilis `golang:1.26-alpine` yang dipin dengan digest. `golang.org/x/net` naik ke v0.63.0 (butuh Go 1.26). Dependency tidak langsung yang ikut naik karena `go get` dicatat di `go.mod`.
2. **OpenTelemetry v1.46.0 untuk sinyal trace dan metric, sinyal log tetap v0.20.0.** Goravel v1.18.1 (`telemetry/instrumentation/log`) tidak dapat dikompilasi dengan `go.opentelemetry.io/otel/log` v0.21 atau lebih baru (`undefined: otellog.Value`), dan otel v1.47 mensyaratkan log v1.47. v1.46.0 dengan log v0.20.0 dapat dikompilasi, lulus semua tes, dan menghapus GO-2026-6505. Tiga kerentanan sinyal log (GO-2026-6508, GO-2026-6615, GO-2026-6974) tetap di `scripts/vuln-baseline.txt` dan dilacak sebagai SEC-15. Exporter telemetry tidak diaktifkan.
3. **Path modul `github.com/lathief/RiskForge`**, sama dengan repositori. Semua import paket aplikasi diganti dari `goravel/...` ke `github.com/lathief/RiskForge/...`; paket framework (`github.com/goravel/...`) tidak berubah. Urutan init yang diandalkan `app/preflight` tetap benar karena `.../app/preflight` tetap terurut sebelum `.../config`.

## Alternatif yang ditolak

| Alternatif | Alasan ditolak |
|---|---|
| Tetap di Go 1.25 dengan baseline | 13 kerentanan pustaka standar (HTTP/2, TLS, x509, MIME) tetap ada tanpa perbaikan di lini 1.25. |
| Goravel dari branch pengembangan (pseudo-version) | Framework yang belum dirilis menjadi fondasi seluruh aplikasi; perubahan yang tidak terdokumentasi lebih berisiko daripada tiga kerentanan pada exporter yang tidak aktif. |
| Fork atau `replace` paket telemetry Goravel | Utang pemeliharaan permanen untuk fitur yang tidak dipakai. |
| Menghapus provider telemetry | `framework/log`, `database/db`, dan `http/client` meng-import paket telemetry, jadi kodenya tetap terhubung dan tetap dilaporkan. |

## Konsekuensi

- `scripts/vuln-baseline.txt` turun dari 17 menjadi 3 baris. Baris itu dihapus saat Goravel merilis versi yang memakai otel 1.47 (branch pengembangannya sudah).
- Pengembang yang menjalankan Go di host butuh Go 1.26.11 atau `GOTOOLCHAIN=auto` (toolchain diunduh otomatis).
- `scripts/coverage.sh` mencetak path paket relatif terhadap modul, jadi tabel `docs/COVERAGE.md` tidak bergantung pada nama modul.
- Output yang tercatat sebelum perubahan ini (misalnya di `docs/BENCHMARK.md`) masih menyebut paket `goravel/...`; itu catatan run lama, bukan perintah.

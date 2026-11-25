# ADR 0026: Pipeline CI, gerbang kualitas berbasis skrip, dan baseline kerentanan

- Status: Accepted
- Tanggal: 2026-11-24
- Tiket: RF-025 (`SYSTEM_DESIGN.md` bagian 5.6.3, 16.2, 16.4, 17.1)

## Konteks

Bagian 5.6.3 mewajibkan CI menolak drift dokumentasi API (lint OpenAPI 3.1, koleksi Postman dibuat ulang dari nol dan dibandingkan byte, parity `(method, path)`, cakupan operationId). Bagian 17.1 menunda `govulncheck` ke CI. RF-025 meminta GitHub Actions, build dan tes HTTP Goravel/Fiber, build image release, runbook Compose, dan pemeriksaan migrasi Artisan, dengan project namespace unik dan database tes sekali pakai. Sampai RF-024 semua pemeriksaan itu dijalankan tangan: `test.sh`, `e2e.sh`, `generate-postman.sh --check`, dan perintah lint. Backlog mencatat SEC-08 (tanpa pemindaian kerentanan), TST-02 (gofmt dan CRLF), M4-TOOL-01 (e2e dan smoke tidak di CI), dan M4-TOOL-03 (tidak ada tes `migrate:rollback`).

## Keputusan

1. **Workflow tipis, skrip tebal.** Setiap job di `.github/workflows/ci.yml` memanggil satu skrip di `scripts/`; `scripts/ci.sh` memanggil skrip yang sama berurutan di mesin pengembang. Tidak ada logika di YAML. Kegagalan di CI direproduksi dengan satu perintah, dan skrip dapat dipakai oleh sistem CI lain.
2. **Gerbang.** `docs` (tautan dan path di Markdown, daftar skrip dan panduan), `contract` (lint tanpa warning, koleksi Postman dibuat ulang dan dibandingkan byte, tes kontrak, lalu `git diff --exit-code api/postman`), `migrations` (naik, turun satu langkah per migrasi, naik lagi, reset, naik, dibandingkan lewat sidik jari skema), `tests` (`test.sh`: gofmt, vet, `-race`, konkurensi diulang), `e2e` (stack baru, 130 pemeriksaan, koleksi Postman), `image` (build dan pemeriksaan image release), dan `vulnerabilities`. Satu job status `CI passed` yang menjadi check wajib branch terlindungi, sehingga menambah job tidak mengubah konfigurasi branch.
3. **Namespace unik dan sekali pakai.** Setiap job membuat project Compose dari id run (`rf-test-ci-<run>-<job>`, `rf-live-ci-<run>`) dengan PostgreSQL baru dan menghapusnya dengan `down -v` pada project itu saja. Tidak ada volume, jaringan, atau database bersama antar job; tidak ada cache modul antar run (lebih lambat beberapa menit, tetapi setiap run independen).
4. **Hak minimum.** `permissions: contents: read`, tanpa secret, tanpa job yang menulis ke repository, registry, atau lingkungan. Action pihak ketiga dikunci ke SHA commit dengan tag di komentar; image kontainer dikunci ke versi atau digest; `govulncheck` ke versi. Dependabot (gomod dikelompokkan, github-actions, docker) mengusulkan pembaruan yang lewat pipeline yang sama.
5. **Kerentanan: baseline, bukan lampu hijau palsu.** Pemindaian pertama menemukan 26 kerentanan yang dapat dijangkau. Menaikkan dependency tidak langsung dalam lini rilisnya menghapus 9 (pgx, x/text, x/net, x/crypto, grpc, brotli, compress; build, vet, dan seluruh tes lulus). 17 sisanya tidak dapat dihapus tanpa keputusan: 13 di pustaka standar Go (5 di antaranya juga di `x/net`) yang perbaikannya hanya ada di Go 1.26.x (lini 1.25 yang dipin ADR 0001 tidak punya perbaikan), dan 4 di OpenTelemetry yang versi perbaikannya mengubah API log yang dipakai Goravel v1.18.1 (build gagal, dicoba). `scripts/vuln-baseline.txt` mencatat setiap ID dengan alasan; pipeline gagal pada temuan **baru** dan melaporkan baris baseline yang sudah tidak ditemukan. Memindahkan toolchain ke Go 1.26 dan meng-upgrade Goravel adalah keputusan terpisah (SEC-11).
6. **Pemindaian image tidak memblokir dulu.** Trivy memindai paket OS image dari berkas tar (tanpa mounting Docker socket, sesuai larangan Bagian 0). Langkah ini ditulis tanpa dapat dijalankan (database Trivy tidak terjangkau dari lingkungan pengembangan), jadi job-nya `continue-on-error` sampai lulus sekali di CI; kode keluar `10` membedakan temuan dari kegagalan menjalankan. **Pembaruan (validasi M5):** job ini lulus di GitHub pada PR #10, lalu `continue-on-error` dihapus dan job ini masuk `needs` dari `CI passed`.
7. **Image release hanya dibangun dan diperiksa, tidak dipublikasikan.** Registry, kredensial, dan kebijakan tag belum diputuskan; `image-check.sh` membuktikan image dapat dirilis (tanpa `.env`, kontrak, atau Swagger UI; non-root; menolak start dengan konfigurasi salah dengan status gagal; migrasi dengan binary-nya sendiri; header dan log ke stdout; berhenti bersih pada SIGTERM). Tidak ada job CD.
8. **Cakupan dokumentasi API diperluas.** Tes kontrak kini juga memeriksa `operationId` unik berbentuk lower camel case, ringkasan unik, satu tag terdeklarasi per operasi, nama dan folder request koleksi sama dengan ringkasan dan tag spec, respons `401`, `403`, `408`, `500` pada operasi terlindungi, dan `429` pada operasi yang dibatasi. `operationId` `me` diganti `getCurrentUser` (OpenAPI 0.20.1).
9. **Perbaikan produksi yang ditemukan pemeriksaan image.** Goravel keluar dengan status 0 bila `APP_KEY` tidak ada, sehingga kontainer tanpa kunci tampak selesai normal. Paket `app/preflight` (diimpor main untuk efek init-nya, yang diurutkan sebelum `goravel/config`) kini menolak start dengan status 1 dan satu baris JSON di stderr; perintah `artisan` dikecualikan karena `key:generate` berjalan sebelum kunci ada.
10. **Format file.** `.gitattributes` memaksa LF untuk `*.go`, `go.mod`, `go.sum`, dan YAML (TST-02).

## Alternatif yang ditolak

| Alternatif | Alasan ditolak |
|---|---|
| Logika langsung di YAML (banyak `run:` panjang, action khusus per alat) | Tidak dapat dijalankan lokal, terkunci pada satu penyedia CI, dan menduplikasi skrip yang sudah ada. |
| Satu job besar berurutan | Lebih lambat, dan satu kegagalan menyembunyikan yang lain; job paralel memberi semua kegagalan sekaligus. |
| `govulncheck` tanpa baseline (gagal pada apa pun) | CI akan merah sejak hari pertama karena 17 temuan yang butuh keputusan toolchain; tim belajar mengabaikan merah. |
| Baseline berupa `continue-on-error` untuk seluruh job | Temuan baru lolos tanpa terlihat. |
| Memaksa upgrade ke Go 1.26 dan Goravel terbaru di tiket ini | Mengubah toolchain, framework, dan ADR 0001 tanpa persetujuan; risiko perilaku di semua modul. |
| Menaikkan hanya sebagian keluarga OpenTelemetry | Dicoba: build gagal karena API log berubah. |
| Cache modul Go lintas run | Menambah keadaan bersama dan risiko paket basi; waktu yang dihemat tidak sebanding. |
| Job publikasi image ke GHCR | Registry dan kredensial belum diputuskan; kode yang tidak dapat diuji di sini tidak boleh memegang hak tulis. |
| Menguji `preflight` dengan membangun binary di tes Go | Menambah menit ke setiap `go test`; `image-check.sh` menguji perilaku itu pada image yang sama dengan yang dirilis, dan logikanya diuji unit (`AppKeyMissing`). |

## Konsekuensi

- Waktu pipeline sekitar 30 menit (job `tests`), paralel dengan yang lain; biaya menit CI nyata. Tes `tests/feature` berjalan sekitar 12 menit dengan `-race`.
- Workflow belum pernah berjalan di GitHub (hanya actionlint dan skrip-skripnya yang dijalankan di sini). Perbedaan lingkungan (kepemilikan berkas bind mount di Linux, batas Docker Hub) mungkin baru terlihat pada run pertama.
- Baseline kerentanan adalah utang yang terlihat; setiap upgrade dependency dapat menghapus barisnya, dan skrip memberi tahu. Sampai SEC-11 diputuskan, 17 kerentanan yang dapat dijangkau (kebanyakan di jalur HTTP/2 dan klien yang tidak dipakai server fasthttp) tetap ada di image.
- Tes kontrak yang lebih ketat dapat gagal untuk operasi baru yang lupa mendokumentasikan `401`, `403`, `408`, `500`, atau `429`; itu disengaja.
- Dependabot akan membuka pull request untuk dependency tidak langsung Goravel yang dapat gagal build bila keluarganya tidak bergerak bersama; dikelompokkan untuk mengurangi kasus itu.

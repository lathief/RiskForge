# ADR 0025: Golden case lewat HTTP, e2e, satu perintah tes, dan benchmark

- Status: Accepted
- Tanggal: 2026-11-24
- Tiket: RF-024 (`SYSTEM_DESIGN.md` bagian 12, 15.3, 16.1, 16.3)

## Konteks

Bagian 16.1 meminta piramida tes (unit, integrasi database, HTTP/E2E, konkurensi, keamanan) dan baseline performa dengan p50/p95/p99 yang dilaporkan terpisah untuk engine saja dan end-to-end. Sampai RF-023 ada 855 tes dan `m4-e2e.sh`, tetapi sepuluh golden case Bagian 12 tersebar di banyak tes tanpa peta, hanya governance yang punya skrip e2e, tidak ada satu perintah untuk seluruh pemeriksaan, `gofmt -l` tidak dapat dipakai di checkout Windows karena CRLF (TST-02), dan satu-satunya benchmark (`BenchmarkResolveAndEvaluate`) bersifat informal.

## Keputusan

1. **Golden case lewat HTTP di satu file bernama sesuai kasus.** `tests/feature/golden_test.go` memuat `TestGolden01` sampai `TestGolden10`, memakai policy `KTA_DEMO` yang di-seed dan jalur lengkap (request, dictionary, features, RAC, scorecard, matrix, presedensi, penyimpanan). Kasus 5 (skor tepat di batas matrix) tidak dapat dicapai oleh scorecard demo (skor yang mungkin 420 sampai 685, tanpa 500 atau 650), sehingga tes mensimulasikan draft dengan scorecard yang memetakan satu usia ke satu skor eksak (499,99; 500,00; 649,99; 650,00). Kasus yang lebih halus tetap di unit test domain; file ini hanya menjaga janji desain end to end.
2. **Satu skrip e2e untuk seluruh API.** `m4-e2e.sh` diganti `e2e.sh`, 130 pemeriksaan dalam lima bagian (M1 sampai M5) mengikuti alur Bagian 16.1. Tanpa dependency di host: `curl`, `grep`, `sed` (tidak ada `jq`). Dengan `--project` skrip membuat dan menghapus stack sekali pakai (nama harus `rf-test-*` atau `rf-live-*`, `down -v` hanya project itu) dan menjalankan koleksi Postman, sehingga RF-025 dapat memanggilnya apa adanya. Tanpa `--project` ia mengubah data stack yang dituju, dan itu tertulis di bantuan skrip.
3. **`scripts/test.sh` sebagai satu perintah.** `gofmt` memakai salinan tanpa CR (TST-02 teratasi untuk jalan lokal), lalu `go vet`, `go test -race ./...` dengan `-timeout 25m`, lalu tes konkurensi diulang (`-count`, bawaan 3). Keluaran penuh disimpan di `storage/test/`.
4. **Benchmark dipisah dua.** Engine-only memakai `go test -bench` pada paket yang bersangkutan (evaluasi policy per jenis hasil, paralel, kompilasi, hash idempotency, metrics, limiter), tanpa HTTP dan database. End-to-end memakai generator beban kecil tanpa dependency (`scripts/bench/load.js`, Node 22, `fetch`, loop tertutup, nearest-rank percentile) di container pada jaringan project, jadi database tidak pernah diekspos ke host. Skrip mencatat lingkungan (mesin, Docker, Go, PostgreSQL, commit), konfigurasi stack, dan puncak CPU dan memori container. Rate limit dimatikan selama pengukuran dan log mengikuti image release secara bawaan, karena yang diukur adalah aplikasi, bukan limiter atau log dev; `--dev-logging` mengukur biaya log.
5. **Compose meneruskan tiga variabel** (`RATE_LIMIT_ENABLED`, `LOG_FILES`, `LOG_HTTP_BODIES`; validasi M5 menambah `HTTP_MAX_IN_FLIGHT`, `HTTP_PROXY_HEADER`, `HTTP_TRUSTED_PROXIES`) dengan bawaan yang sama dengan aplikasi, supaya skrip dapat mengaturnya tanpa mengubah `up` biasa.
6. **Angka dicatat, tidak dijadikan gerbang.** `docs/BENCHMARK.md` memuat satu run per konfigurasi dengan peringatan variasi dan tanpa SLO; tidak ada tes yang gagal karena latensi.

## Alternatif yang ditolak

| Alternatif | Alasan ditolak |
|---|---|
| Tool beban eksternal (k6, wrk, vegeta) | Menambah image dan versi untuk dipin; yang dibutuhkan hanya loop tertutup, histogram, dan persentil. |
| Mengukur dari host lewat port yang dipublikasikan | Menambah NAT Docker Desktop dan CPU `curl`; di jaringan project hanya jalur aplikasi yang diukur. |
| `go test` dengan benchmark HTTP in-process | Tidak melewati driver Fiber nyata dan koneksi database; tidak mewakili end to end. |
| Gerbang performa di CI | Satu mesin, satu run, tanpa baseline stabil; hasilnya akan flaky. Diputuskan setelah ada data berulang. |
| Memakai `jq` di `e2e.sh` | Dependency host tambahan; pemeriksaan teks cukup untuk status dan field yang dicari. |
| Memperbaiki biaya query per request dalam tiket ini | Tiket ini mengukur; optimasi mengubah jalur request dan butuh ADR sendiri (REL-08). |

## Konsekuensi

- Pemeriksaan teks pada `e2e.sh` dapat lolos atau gagal karena perubahan format JSON yang tidak berarti; keuntungannya skrip berjalan di mesin apa pun dengan Bash dan `curl`. Golden test Go adalah pemeriksaan yang lebih ketat.
- `e2e.sh` menjaga rate limit tetap menyala karena bagian M5 memeriksanya; menjalankannya berulang kali pada stack yang sama dalam satu menit dapat menghabiskan anggaran login (burst 10).
- Benchmark berjalan di mesin pengembang dengan generator beban di mesin yang sama; angka hanya sah untuk membandingkan perubahan pada mesin itu.
- Temuan: `test.sh` menemukan file RF-021 yang tidak gofmt bersih; round trip database mendominasi latensi (REL-08); log dev mahal (REL-07).

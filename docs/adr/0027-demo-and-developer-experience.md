# ADR 0027: Demo yang dapat dijalankan ulang, fixture sintetis, dan dokumentasi pengembang

- Status: Accepted
- Tanggal: 2026-11-24
- Tiket: RF-026 (`SYSTEM_DESIGN.md` bagian 17 gerbang M5, 18, 20)

## Konteks

Gerbang M5 meminta demo end-to-end yang dapat direproduksi dari project Compose baru dengan database baru. Dokumen yang ada (`docs/API_EXAMPLES.md`, `docs/GETTING_STARTED.md`) menjelaskan satu endpoint per bagian, tetapi tidak ada satu cerita dari awal sampai akhir, dan contoh yang ditulis tangan dapat usang tanpa ada yang tahu. `scripts/e2e.sh` sudah menjalankan alur penuh, tetapi ia adalah pemeriksaan (130 baris `ok`), bukan penjelasan.

## Keputusan

1. **Demo adalah skrip yang memeriksa dirinya sendiri.** `scripts/demo.sh` menjalankan dua belas langkah (login enam peran, policy aktif, lima pemohon, replay idempotensi, draft kebijakan yang lebih ketat, simulasi, diff, submit, dua tingkat approval, keputusan berikutnya, audit, metrics). Setiap langkah mencetak request dan field penting dari respons nyata, dan berhenti dengan kode 1 bila respons berbeda dari yang diharapkan cerita. Output di `docs/DEMO.md` adalah hasil satu run, bukan teks yang ditulis tangan.
2. **Project sekali pakai.** `--project rf-live-<nama>` membuat stack baru di port 3300, migrate, seed, menjalankan cerita, dan menghapus project itu dengan `down -v`; nama harus berawalan `rf-test-` atau `rf-live-`, sama seperti `e2e.sh`. Tanpa `--project` skrip mengubah data stack yang dituju dan run kedua tidak mulai dari keadaan yang sama (kebijakan sudah memakai usia 30), jadi dokumen dan skrip menyebutnya.
3. **Fixture sintetis di `scripts/demo/applications/*.json`.** Lima berkas, masing-masing badan lengkap untuk `POST /decisions/evaluate`: contoh kerja desain (bagian 20.4), REFER karena SLIK, REJECT karena usia, REJECT karena blacklist, dan input tidak valid. Hasil yang diharapkan dicek di dua tempat: `demo.sh` pada stack nyata dan tes `TestDemoFixturesGetTheirDocumentedOutcome` pada setiap pull request, sehingga perubahan kebijakan seed atau dictionary tidak dapat merusak demo tanpa terlihat.
4. **Tanpa `jq`.** Skrip hanya memakai `curl`, `grep`, `sed`, seperti `e2e.sh`, agar tidak ada prasyarat baru di mesin pengembang. Harganya: field diambil dengan pola teks, bukan parser JSON (TST-07 berlaku juga di sini).
5. **Diagram dan README mengikuti kode.** README mendapat bagian "See it work", status dan roadmap yang diperbarui, dan tautan ke `docs/DEMO.md`. `docs/ARCHITECTURE.md` mendapat diagram dari perubahan ke rilis terverifikasi (gerbang CI), dan penanda "planned" untuk audit search dihapus karena sudah ada. Diagram Mermaid baru tidak dapat dirender di sini (image `mermaid-cli` tidak dapat diunduh); sintaksnya sederhana dan label bertanda titik dua diberi tanda kutip.
6. **Tanpa tangkapan layar.** Tiket menyebut "screenshots/API sample output"; yang disediakan adalah output API nyata. Tangkapan layar Swagger UI memerlukan peramban, dan berkas gambar akan usang pada setiap perubahan spec tanpa pemeriksaan.

## Konsekuensi

- Demo dapat dijalankan oleh orang baru dengan satu perintah setelah `init-agent-env.sh`; run pertama lambat (kompilasi) dan butuh jaringan untuk menarik image.
- `docs/DEMO.md` perlu dibuat ulang bila output berubah. Tidak ada pemeriksaan otomatis bahwa dokumen sama dengan output skrip; yang diperiksa adalah bahwa skrip lulus (dijalankan manual, belum menjadi gerbang CI) dan bahwa fixture mendapat hasil yang benar (tes, di CI).
- Menambahkan `demo.sh` sebagai gerbang CI akan menambah satu stack baru per run; tidak dilakukan karena `e2e.sh` sudah membuktikan alur yang sama dan biaya waktunya tidak sebanding.

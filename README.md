# Smart Campaign Composer

Pazarlamacı kampanyayı doğal dille anlatır. Claude bu özetten bir kitle kuralı ve SMS / e-posta metinleri önerir. Backend bunları doğrular, pazarlamacı ekranda düzenleyip onaylar, mesajlar kuyruk üzerinden sahte bir sağlayıcıya gider ve gönderim canlı panoda izlenir.

```
"Son 30 günde sepete ürün ekleyip satın almayan İstanbullu kullanıcılara %15 indirim kodu gönder."
        │
        ▼
Kitle kuralı  city in [İstanbul] ∧ cart_add son 30 gün ∧ ¬purchase son 30 gün  →  412 kişi
Mesajlar      SMS (≤160), e-posta konusu (≤80), ön izleme metni, gövde; TR + EN
Onay          kanallar, hemen / ileri tarih
Gönderim      kanal başına ≤100 mesaj/sn, yeniden deneme, mükerrer gönderim yok
```

## Hızlı başlangıç

Gereken tek şey Docker (Compose v2).

```bash
cp .env.example .env      # isteğe bağlı; anahtar olmadan da çalışır
docker compose up --build # ya da: make up
```

| Adres | Ne |
|---|---|
| http://localhost:3000 | Arayüz |
| http://localhost:8080/api | API |
| http://localhost:8080/healthz, `/readyz`, `/metrics` | Sağlık, hazır olma, Prometheus metrikleri |
| http://localhost:8090/stats | Sahte sağlayıcının sayaçları (`duplicate_deliveries` dahil) |

İlk açılışta `seed` servisi migration'ları çalıştırır. Ardından 4.000 sahte kullanıcı ve 180 günlük davranış verisi (sayfa görüntüleme → ürün inceleme → sepete ekleme → satın alma hunisi) üretir. Bütün e-postalar `@example.com`, telefonlar `+90555…` aralığında; gerçek bir kişiye ulaşılamaz. Veritabanı doluysa seed atlanır.

### Claude ile ya da Claude olmadan

- **`ANTHROPIC_API_KEY` dolu:** Kitle ve mesajları Claude üretir. Varsayılan model `claude-opus-5-5`; `ANTHROPIC_MODEL` ve `ANTHROPIC_EFFORT` ile değiştirilebilir.
- **Anahtar yok:** Yerleşik demo modu devreye girer. Anahtar kelime kurallarıyla kitle ve hazır şablonlardan mesaj üretir, akışı aynı şekilde stream eder. Arayüzün sol altında bunu belirten bir not çıkar. Böylece proje anahtarsız ve internetsiz de denenebilir.

Her iki yolun çıktısı da aynı doğrulamadan geçer.

### Testler

```bash
make test
```

- **Backend:** Önce `go vet`, sonra `go test -race` çalışır. Entegrasyon testleri `docker-compose.test.yml` ile ayağa kalkan geçici Postgres ve Redis'e karşı koşar; bu testler bitince silinir.
- **Frontend:** `vue-tsc` tip kontrolü ve `vitest`.

Testler gerçek bir LLM çağırmaz. Composer testleri, sırayla hazır cevaplar döndüren `llm.Scripted` ile çalışır: geçersiz JSON, şemaya uymayan kural, sınırı aşan SMS gibi durumlar ve onarım döngüsü bu şekilde sınanır.

Entegrasyon testleri:

- **`TestCampaignDeliversExactlyOnce`:** Hata enjeksiyonu açıkken (429, 5xx, zaman aşımı) iki worker aynı kampanyayı gönderir. Her (kullanıcı, kanal) çiftine tam bir mesaj gittiği doğrulanır.
- **`TestCancelStopsPendingDeliveries`:** İptal edilen kampanyadan bekleyen mesaj çıkmadığını sınar.
- **`TestLimiterSpacesSendsAcrossWorkers`:** Birden çok worker'ın toplamda saniyede 100 sınırını aşmadığını ölçer.

CI aynı adımları GitHub Actions'ta çalıştırır (`.github/workflows/ci.yml`) ve tüm image'ların derlendiğini de kontrol eder.

## Mimari

```
              ┌──────────── web (Vue 3, nginx) ────────────┐
              │  özet → kitle → mesajlar → onay → canlı pano │
              └───────────────┬─────────────────────────────┘
                              │ /api  (JSON + SSE)
                       ┌──────▼──────┐         ┌───────────┐
                       │     api     │────────▶│  Claude   │
                       │  (Go, HTTP) │ stream  │ (ya da    │
                       └──┬───────┬──┘         │  demo)    │
                 doğrulanmış      │            └───────────┘
                 kural/metin      │ onay
                          │       │
                   ┌──────▼───────▼──────┐
                   │      Postgres       │  kullanıcılar, olaylar, kampanyalar,
                   │  (tek doğruluk      │  teslimatlar (durum makinesi),
                   │   kaynağı)          │  LLM çağrı kaydı
                   └──────▲───────┬──────┘
                          │       │ zamanı gelen kampanya → teslimat satırları
                   ┌──────┴───────▼──────┐        ┌──────────────────┐
                   │       worker        │◀──────▶│      Redis       │
                   │ zamanlayıcı, kanal  │        │ stream (kanal    │
                   │ başına tüketiciler, │        │ başına), retry   │
                   │ uzlaştırıcı         │        │ ZSET, GCRA sayacı│
                   └──────────┬──────────┘        └──────────────────┘
                              │ HTTP + Idempotency-Key
                       ┌──────▼──────┐
                       │ mockprovider│  gecikme, 429, 5xx, zaman aşımı enjekte eder
                       └─────────────┘
```

### 1. Özetten kurala

`composer` paketi Claude'a kitle kataloğunu verir: alanlar, izin verilen değerler, olay türleri ve kategoriler. Claude yalnızca yapılandırılmış JSON döndürür (Structured Outputs).

Kural SQL değildir; şu üç koşul türünden oluşan küçük bir AST'dir:

- `text`: alan, değer listesinin içinde ya da dışında.
- `number`: karşılaştırma ya da `between`.
- `event`: belirli bir olay son N günde (belirli bir kategoride, en az K kez) olmuş ya da olmamış.

Backend kuralı katalogla karşılaştırarak doğrular. Ardından beyaz listedeki ifadelerden, değerleri parametre olarak geçen bir SQL'e derler (`segment/compile.go`). Modelin ürettiği hiçbir metin SQL'e girmez.

Doğrulama başarısız olursa hatalar alan yoluyla birlikte modele geri gönderilir, örneğin `conditions[1].within_days: must be ≤ 365`. Bu onarım döngüsü en fazla `LLM_MAX_ATTEMPTS` kez denenir. Geçerli bir kural çıkmazsa kampanyaya hiçbir şey kaydedilmez.

### 2. Mesajlar

Mesajlar kitlenin dillerinde üretilir (`tr`, `en`). Her mesaj stream edilirken ekrana yazılır; yarım JSON'u toleranslı ayrıştırıcı `llm/partialjson.go` okur.

İçerik doğrulamasında uygulanan kurallar:

- **Değişkenler:** Yalnızca `{{first_name}}`, `{{last_name}}` ve `{{city}}` kullanılabilir.
- **SMS:** En fazla 160 karakter. Sınır, değişkenlere kitledeki en uzun isim ve şehir yerleştirildiğinde de aşılmamalı.
- **E-posta:** Konu en fazla 80, ön izleme metni en fazla 140, gövde en fazla 4.000 karakter.

Son denemede de geçmeyen içerik kaybolmaz. Taslak olarak kaydedilir, sorunlu alanlar düzenleyicide işaretlenir ve onaydan önce yeniden doğrulanır.

Claude'un her çağrısı `llm_calls` tablosuna yazılır: amaç, deneme numarası, model, prompt sürümü, sonuç, hatalar, token sayısı ve süre. Arayüzde "Claude çağrı kaydı" bölümünde görünür.

### 3. Onay

Onay insan kapısıdır. Kural ve içerik o anki veriye göre yeniden doğrulanır, kitle yeniden sayılır. Seçili kanallarda izni olan kimse yoksa onay reddedilir. Kampanya `scheduled` durumuna geçer; hemen gönderim, `scheduled_at = şimdi` anlamına gelir.

### 4. Gönderim

Worker'daki zamanlayıcı her saniye zamanı gelen kampanyayı `FOR UPDATE SKIP LOCKED` ile alır. Aynı transaction içinde kampanyayı `sending` durumuna geçirir ve izinli her (kullanıcı, kanal) çifti için bir `deliveries` satırı yaratır. Kitle bu anda yeniden hesaplanır.

Teslimat id'leri kanal başına Redis Stream'e (`scc:stream:sms`, `scc:stream:email`) yazılır. Her mesaj şu adımlardan geçer:

1. **Claim:** Korumalı bir `UPDATE` satırı `queued` ya da `retrying` durumundan `sending` durumuna geçirir ve `attempts` sayacını artırır. Kimin göndereceğine yalnızca bu adım karar verir.
2. **Hız sınırı:** GCRA limiter'dan slot alınır. Bu, Redis'te atomik çalışan bir Lua betiğidir ve kanal başına saniyede 100 sınırını tüm worker'lar için ortak uygular.
3. **İzin kontrolü:** Gönderimden hemen önce yeniden yapılır. İzin geri çekildiyse satır `skipped` olur.
4. **Gönderim:** Değişkenler doldurulur ve mesaj, teslimat başına sabit bir `Idempotency-Key` ile sağlayıcıya gönderilir.
5. **Sonuç:**
   - Başarı → `sent`.
   - Kalıcı hata ya da son deneme → `failed`.
   - Geçici hata (429, 5xx, zaman aşımı) → `retrying`. Satır, tam jitter'lı üstel bekleme süresiyle `scc:retry` ZSET'ine park edilir; `Retry-After` alt sınır olarak kullanılır.

   Sonuç yalnızca `attempts` hâlâ aynıysa yazılır. Bu sayaç bir fencing token görevi görür.

Arka plandaki döngüler:

| Döngü | Sıklık | Görevi |
|---|---|---|
| Zamanlayıcı | 1 sn | Zamanı gelen kampanyayı başlatır. |
| Retry promoter | 250 ms | Zamanı gelen yeniden denemeleri stream'e geri taşır. |
| Uzlaştırıcı | 5 sn | Kayıp stream girdilerini yeniden kuyruğa alır, gönderim sırasında ölen worker'ın satırını `retrying` durumuna çeker, biten kampanyaları `completed` yapar. |
| Kuyruk ölçer | 2 sn | Kuyruk derinliğini metrik olarak yayınlar. |

`XAUTOCLAIM`, onaylanmamış stream girdilerini başka bir tüketiciye devreder.

### 5. Canlı pano

`GET /api/campaigns/{id}/events` her saniye Postgres'ten bir anlık görüntü stream eder; biten kampanyalarda bu aralık 5 saniyedir. İçerdikleri:

- Kanal ve durum başına sayılar.
- Deneme ve yeniden deneme toplamları.
- Kuyruk derinliği.
- Sağlayıcı istatistikleri.

Pano bu görüntülerden şunları çizer: kanal başına ilerleme çubuğu, saniyedeki gönderim ve sınır çizgisi, teslimat tablosu, sağlayıcı tarafındaki hata sayıları ve "mükerrer teslimat" sayacı.

## API

| Yöntem | Yol | |
|---|---|---|
| GET | `/api/meta` | Katalog, limitler, değişkenler, LLM sağlayıcısı |
| GET | `/api/campaigns` | Liste |
| POST | `/api/campaigns` | `{brief}` ile taslak oluşturur |
| GET | `/api/campaigns/{id}` | |
| POST | `/api/campaigns/{id}/generate` | SSE: `status`, `validation`, `content.partial`, `segment`, `content`, `error`, `done` |
| PUT | `/api/campaigns/{id}/segment` | Elle düzenlenen kural (doğrulanır, kitle yeniden sayılır) |
| POST | `/api/segments/preview` | Kaydetmeden kitle sayımı |
| PUT | `/api/campaigns/{id}/content` | Elle düzenlenen içerik |
| POST | `/api/campaigns/{id}/content/check` | Kaydetmeden doğrulama ve SMS kodlama/parça bilgisi |
| POST | `/api/campaigns/{id}/approve` | `{channels, scheduled_at?}` |
| POST | `/api/campaigns/{id}/cancel` | |
| GET | `/api/campaigns/{id}/stats` | Anlık durum (JSON) |
| GET | `/api/campaigns/{id}/events` | Anlık durum (SSE) |
| GET | `/api/campaigns/{id}/deliveries?status=` | Teslimat satırları |
| GET | `/api/campaigns/{id}/llm-calls` | LLM çağrı kaydı |
| GET | `/api/stats` | Tüm sistem özeti |

Hatalar tek biçimde döner: `{"error": {"code", "message", "details"}}`. Doğrulama hatalarında `details`, alan yolu ve mesaj listesidir.

## Yapılandırma

Bütün ayarlar `.env.example` içinde açıklamalarıyla listelenmiştir. En önemlileri:

| Değişken | Varsayılan | |
|---|---|---|
| `ANTHROPIC_API_KEY` | boş | Boşsa demo modu kullanılır |
| `ANTHROPIC_MODEL` | `claude-opus-5-5` | |
| `LLM_MAX_ATTEMPTS` | 3 | Doğrula-onar döngüsü |
| `RATE_PER_SECOND` | 100 | Kanal başına |
| `DELIVERY_MAX_ATTEMPTS` | 8 | |
| `RETRY_BASE_DELAY` / `RETRY_MAX_DELAY` | 500ms / 30s | |
| `MOCK_429_RATE`, `MOCK_500_RATE`, `MOCK_TIMEOUT_RATE` | 0.03 / 0.03 / 0.01 | Sağlayıcı hata enjeksiyonu |

## Proje yapısı

```
backend/
  cmd/{api,worker,seed,mockprovider}   dört süreç, tek image
  internal/
    segment/     kural AST'i, katalog, doğrulama, SQL derleyici
    content/     içerik modeli, limitler, SMS GSM-7/UCS-2 analizi, render
    composer/    prompt'lar, doğrula-onar döngüsü, stream olayları
    llm/         Client arayüzü; anthropic.go, demo/, scripted.go (testler)
    campaign/    kampanya deposu ve servis kuralları (düzenleme, onay)
    delivery/    durum makinesi, Redis Streams kuyruğu, GCRA, backoff, worker
    provider/    sağlayıcı istemcisi ve sahte sağlayıcı
    api/         HTTP ve SSE uçları
    db/          bağlantı havuzu, goose migration'ları
    itest/       Postgres + Redis entegrasyon testleri
frontend/        Vue 3 + TypeScript + Vite
DECISIONS.md     tasarım kararları ve ödünleşimler
```

# Claude Code Session Export — 1/2

- **Tarih:** 17 Kasım 2026, 13:02 – 23:47
- **Proje:** /Users/ergenekon/ayri-is
- **Konu:** Brief'e bakış, yığın seçimi, süreç mimarisi, kitle kuralı DSL'i, LLM katmanı

> Dışa aktarım; API anahtarları ve kişisel veri içermez.

---

## User:
kral selam, sana yeni bi task vercem jobapplier ile alakası yok bu. insider diye bi şirketin ai developer case study'si, "smart campaign composer" diyor. brief'i okuyup önce konuşalım ne yapcaz nasıl yaklaşcaz

## Claude:
Selam! Hemen bakalım brief'e. Önce task'ı doğru anlamamız lazım, sonra mimariyi konuşuruz.

Brief'i okudum. Özetlemeye çalışayım:

**Smart Campaign Composer.** Pazarlamacı kampanyayı doğal dille anlatıyor:

> "Son 30 günde sepete ürün ekleyip satın almayan İstanbullu kullanıcılara %15 indirim kodu gönder."

Claude bundan iki şey üretiyor:
1. **Kitle kuralı** — segment tanımı
2. **SMS / e-posta metinleri** — TR + EN varyantlar

Ardından insan onaylıyor, mesajlar kuyruğa giriyor, sahte bir sağlayıcıya gidiyor ve gönderim canlı panoda izleniyor.

**Zorunlu gibi görünen kısımlar:**
- Aday akışı: brief → rule + content → approve → send (streaming arayüzde)
- Hedef kanallar: SMS + e-posta
- Sınır: kanal başına 100 msg/sn
- Yeniden deneme, mükerrer gönderim yok, ileri tarihli gönderim, iptal
- `docker compose up` ile her şey ayağa kalkmalı
- Bonuslar: çok dillilik, canlı pano, A/B

Teslim tarihi 7 gün ve sonrasında 30 dakikalık bir değerlendirme görüşmesi var — yani kodu savunabilmemiz de gerekiyor. Bu yüzden sadece kod değil, kararların *nedenleri* de baştan yazılmalı. `DECISIONS.md` tutalım, her kararın başında neyi seçtiğimizi neyi neden elediğimizi yazayım mı?

## User:
evet decisions da tutalım ya önemli olan ben o gorusmede savunabileyim. once yigin ne olsun konusalim

## Claude:
Yığın seçimini sistemin zor kısmından başlatmak istiyorum. Bu projede "zor" olan kısım UI değil, şu:

- kanal başına onlarca eşzamanlı tüketici
- hız sınırı (100/sn, worker'lar arasında *ortak*)
- yeniden deneme takvimi
- iptal
- "aynı mesaj iki kez gitmesin" garantisi

Yani asıl mesele **eşzamanlı gönderim motoru**. Buna göre:

**Go öneriyorum.** Gerekçeler:
1. Bu tip eşzamanlılık Go'nun anavatanı: goroutine + `context` + `sync`, framework'süz standart kütüphaneyle yazılır.
2. Tek statik binary'den 4 süreç çıkar (`api`, `worker`, `seed`, `mockprovider`) — image küçük, deploy basit.
3. Anthropic'in resmî Go SDK'sı var; streaming ve Structured Outputs destekliyor.
4. Race detector ile eşzamanlılık testi yazmak çok kolay.

Alternatifler:
- **Node/TS:** hızlı geliştirilir ama eşzamanlı motor için callback/kuyruk kütüphaneleriyle boğuşursun, worker süreç modeli daha hantal.
- **Python:** CPU değil ama dağıtık koordinasyon tarafında (fencing, atomic claim) tooling zayıf kalır.
- **Rust:** doğru ama 7 günde teslim + sonradan başkasının okuyup tartışacağı kod için maliyeti yüksek.

Sen frontend'de rahatsın; arka planda Go bence en defansı yazılabilir olan.

## User:
go olsun abi sen bilrsn. peki db ve cache ne olsun

## Claude:
**Postgres + Redis** ikilisini öneriyorum, ikisi farklı işi yapıyor:

**Postgres = tek doğruluk kaynağı.**
- Kitle kuralı aslında ilişkisel bir sorgu: kullanıcı alanları + "son N günde şu olay oldu/olmadı" alt sorguları. Bu doğal olarak SQL.
- Teslimat durumu (`queued → sending → sent/failed/retrying/skipped`) transaction ve satır kilidiyle ancak güvenli yönetilir. `UPDATE ... WHERE status IN (...)` bir satırı yalnızca tek bir tüketici claim edebilir.
- İki kez başlatılan kampanyayı `UNIQUE (campaign_id, user_id, channel)` ile veritabanı seviyesinde keseriz.

**Redis = koordinasyon katmanı, veri değil.**
- Kanal başına iş kuyruğu: **Streams** (consumer group ile).
- Yeniden deneme takvimi: **ZSET** (`scc:retry`, skoru zaman).
- Süreçler arası ortak hız sınırı: **Lua** ile atomik script.

Kritik prensip: **Stream'e sadece teslimat id'si yazılır.** Mesaj metni, kullanıcı, deneme sayısı Postgres'te. Bunun faydaları:

1. Redis kaybı = veri kaybı değil. Kaybolan kuyruğu uzlaştırıcı (reconciler) Postgres'ten yeniden kurar: `queued`/`retrying` durumunda kalan satırları tekrar stream'e koyar.
2. Pano da worker da aynı kaynağa bakar; kuyruk ayrı bir "gerçeğe" dönüşmez.

Bedeli: her gönderimde bir claim + bir sonuç yazması, yani iki Postgres yazması. 100 msg/sn'de önemsiz. Saniyede on binlere çıksaydı sonuçları toplu yazardık — şimdi gereksiz karmaşıklık olurdu.

Redis'i "teslimatın kendisi" yapsaydık (payload Redis'te) ne olurdu, konuşalım istersen: kod basitleşirdi ama Redis kaybında kuyruktaki mesajlar giderdi, pano için ikinci bir durum kaynağı doğardı. Uzlaştırma da imkânsızlaşır çünkü neyi kaybettiğini bilmezdin. Bu yüzden "id taşı, yükü DB'de tut" daha savunulabilir.

## User:
tamam db postgres redis. frontendte ne kullancaz react mi

## Claude:
React de olurdu ama burada **Vue 3 + Vite** daha uygundu:

- Arayüz tek sayfa ve az durumlu: özet yazma → kitle önizleme → metin düzenleme → onay → canlı pano. Beş ekran, bir akış.
- Asıl dinamik kısım **stream edilen kısmi içerik** (Claude yazarken mesajların ekrana düşmesi). Vue'nun reaktivitesi bunu doğal karşılıyor; ek durum kütüphanesi (Redux/Zustand/TanStack) gerektirmiyor.
- Vite ile build hızlı ve nginx'e statik servis etmek basit.

React'te de aynı şey yapılırdı, fark küçük; ama "ekstra tooling yok, framework ne veriyorsa onunla" hedefi Vue ile daha rahat sağlanıyor. Bu kararı DECISIONS.md'ye "ikisi de olurdu, az durum + streaming için Vue reaktivitesi yeterli" diye yazalım.

## User:
hobey kanka. kod tarafina gecmeden once genel plan ne nasil ilerlicez

## Claude:
Plan şu — önce mimariyi kafamızda netleştirmiş olalım, sonra kod:

```
              ┌──────────── web (Vue 3, nginx) ────────────┐
              │  özet → kitle → mesajlar → onay → canlı pano │
              └───────────────┬────────────────────────────┘
                              │ /api  (JSON + SSE)
                       ┌──────▼──────┐         ┌───────────┐
                       │     api     │────────▶│  Claude   │
                       │  (Go, HTTP) │ stream  │ (ya da    │
                       └──┬───────┬──┘         │  demo)    │
                 doğrulanmış      │            └───────────┘
                 kural/metin      │ onay
                          │       │
                   ┌──────▼───────▼──────┐        ┌──────────────────┐
                   │      Postgres       │◀──────▶│      Redis       │
                   │  (tek doğruluk      │ id-only│ Streams / ZSET / │
                   │   kaynağı)          │        │ Lua hız sınırı   │
                   └──────────▲───────┬──┘        └──────────────────┘
                              │       │ zamanı gelen kampanya → teslimat satırları
                       ┌──────┴───────▼──────┐
                       │       worker        │──▶ mockprovider :8090
                       └─────────────────────┘
```

Dört süreç tek binary'den: `api`, `worker`, `seed`, `mockprovider`.

Devam etmeden kritik bir konu: **kitle kuralını Claude'a nasıl ürettireceğiz?**

Kolay yol: Claude'a "bu brief'e göre SQL yaz" demek. Ama iki sebepten kabul edilemez:

1. **Güvenlik:** prompt enjeksiyonuyla `DELETE`, dev sorgular, `pg_sleep` — LLM çıktısı asla doğrudan SQL olmamalı.
2. **Şeffaflık:** pazarlamacı çıkan kuralı anlayabilmeli ve arayüzde elle düzeltip kitle sayısının değiştiğini görebilmeli. SQL bunu imkânsız yapar.

Önerim: **küçük bir JSON DSL.**

```json
{
  "all": [
    { "type": "text", "field": "city", "op": "in", "values": ["İstanbul"] },
    { "type": "event", "field": "cart_add", "op": "occurred", "within_days": 30 },
    { "type": "event", "field": "purchase", "op": "not_occurred", "within_days": 30 }
  ]
}
```

- `all` / `any` birleştiricileri; `text`, `number`, `event` koşul tipleri.
- **Katalog:** alanlar, operatörler, izinli değerler, sınırlar (`within_days ≤ 365`) tek yerde tanımlı. Katalog hem Claude'nun prompt'una hem backend doğrulayıcısına giriyor — prompt ile doğrulama birbirinden kopamaz.
- **Derleyici:** sadece sabit SQL parçalarını birleştirir, değerler `$n` parametresi olarak gider. Enjeksiyon yüzeyi sıfır.
- Katalogda olmayan istek ("son 48 saatte erken erişim" gibi) sessizce yaklaşıma çevrilmez; Claude `unsupported` notu döner, arayüz gösterir. Yaklaşık kitle göndermek, kuralı savunamamaktan beter.

Bu yapı `backend/internal/segment/` altına düşecek: `catalog.go`, `schema.go`, `rule.go`, `compile.go`, `store.go`.

## User:
tamam da llm baglantisini nasil kurcaz yani ilerde baska model olursa ne olucak bu kisim

## Claude:
İyi soru, baştan doğru kurmak lazım. Şöyle düşünüyorum:

- **OpenAI-compatible format** sektörün lingua franca'sı; ama bu projede tek model kullanacağız ve projenin adı "Claude ile kampanya" gibi bir şey — brief zaten Claude'a işaret ediyor.
- Öte yandan "belki başka sağlayıcı da eklenir" ihtimalini kapatmayalım: katmanı bir arayüzün arkasına koyarız, OpenAI uyumlu istemci sonradan eklenebilir. Ama kapsam dışı tutalım — 7 gün içinde gerçekten kullanılmayacak kod yazmak savunmasız harcama.

Yani: **Claude olsun**, ama izole olsun.

```
backend/internal/llm/
  llm.go        → Client arayüzü (Compose, Stream)
  anthropic.go  → gerçek istemci (streaming + Structured Outputs)
  scripted.go   → testlerde sırayla hazır cevap
  demo/demo.go  → anahtar yoksa anahtar kelime sezgileri
  partialjson.go → stream'den kısmi JSON ayrıştırma
```

`demo` modunun sebebi: değerlendirenin elinde anahtar olmayabilir ya da ortam internetsiz olabilir. `docker compose up` her durumda akışın tamamını göstermeli — demo modu brief'teki örnek kampanyaları makul kurallara çevirir, sahte stream eder, arayüz bunu açıkça belirtir. Testler de `Scripted` ile ağa çıkmadan deterministik çalışır: geçersiz JSON, şema dışı kural, onarım döngüsü...

Kararı netleştirmiş olalım: Claude, ama izole. Şimdi şemayı yazayım mı? Migration'lar ve seed'i konuşalım: 4.000 sahte kullanıcı, 180 günlük davranış verisi (sayfa görüntüleme → ürün inceleme → sepete ekleme → satın alma hunisi). Bütün e-postalar `@example.com`, telefonlar `+90555...` — gerçek kişiye ulaşılamaz, bu brief'in de beklentisi.

## User:
yaz kanka bitince haber ver deneyim

## Claude:
Migration'lar, `internal/seed`, `internal/db`, `internal/config`, `internal/httpx` hazır. Bir yandan `docker-compose.yml`: `web` (nginx), `api`, `worker`, `seed`, `mockprovider`, `postgres`, `redis`. `.env.example` de koydum — anahtar opsiyonel, anahtar yoksa demo mod devreye giriyor.

Portlar şöyle:
- **3000** → arayüz (Vue, nginx üzerinden)
- **8080** → `/api`, ayrıca `/healthz`, `/readyz`, `/metrics`
- **8090** → sahte sağlayıcının sayaçları (`/stats`)

Not: bu makinada lokal Go yok gibi duruyor, o yüzden Go'yu Docker'dan çalıştırıyorum (`golang:1.27`). `make up` ile deneyebilirsin.

## User:
ok kanka actim bakiyorum

## Claude:
3000'den başla: arayüz açılır, migration + seed otomatik çalışır (veritabanı doluysa seed atlanır). Brief'e bir örnek metin yazıp kuralın ve kitle sayısının gelmesini izle. Şu an Claude kısmı `demo` modunda — anahtarı `.env`'e koyarsan gerçek modele döner.

Bu sırada arayüzü de ele aldım: metinleri azaltıp hiyerarşiyi netleştirdim, tek sütun akışta onay adımını belirginleştirdim; tipografik ölçek, tutarlı spacing, sınırlı renk paleti. Pano ekranında "hız sınırı çizgisi vs gerçek gönderim hızı" yan yana görünecek.

Aklında takılan ne varsa konuşalım. Yarının gündemi bence şu: mükerrer gönderim, hız sınırı, yeniden deneme — yani gönderim motorunun kalbi.

Günün özeti — DECISIONS.md'ye yazdıklarım:
1. **Yığın:** Go, Postgres, Redis, Vue 3 + Vite (nedenleriyle).
2. **Postgres tek doğruluk kaynağı; Redis yalnızca id taşır.** Redis kaybı veri kaybı değil; uzlaştırıcı kuyruğu yeniden kurar.
3. **Kitle kuralında LLM SQL yazmaz:** JSON DSL + katalog + parametreli derleyici.
4. **LLM katmanı izole:** `llm.Client` arayüzü; gerçek / demo / scripted üç gerçekleştirme. OpenAI uyumlu istemci bilinçli olarak kapsam dışı.
5. **Demo modu:** anahtar yoksa akış yine de uçtan uca çalışır.

Yarın: exactly-once, GCRA hız sınırı, full-jitter yeniden deneme.

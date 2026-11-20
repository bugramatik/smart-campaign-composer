# Claude Code Session Export — 2/2

- **Tarih:** 18 Kasım 2026, 11:14 – 02:03
- **Proje:** /Users/ergenekon/ayri-is
- **Konu:** Mükerrer gönderim, hız sınırı (GCRA), yeniden deneme, doğrula-onar döngüsü, canlı pano, sahte sağlayıcı, bonuslar ve bilinçli ödünler

> Dışa aktarım; API anahtarları ve kişisel veri içermez.

---

## User:
gunaydin kanka duns konustuklarimizin ustune bugun gonderim motorunu konusalim ya mukerrer gonderim nasil olmucak bu

## Claude:
Gunaydın! Evet, bugün kalp kısmı: **"aynı (kampanya, kullanıcı, kanal) üçlüsüne mesaj bir kez gitsin."**

Önce dürüst tespit: ağ üzerinden gerçek "exactly-once" yoktur. Gönderdik, cevabı almadan öldük — bu pencereyi hiçbir yerel numaralandırma kapatmaz. O yüzden hedefi biraz kaydırıyoruz: **her teslimatın sağlayıcıda bir kez *kabul edilmesi*.** Bunu üç bağımsız katman birlikte sağlıyor:

**Katman 1 — veritabanı kısıtı.**
```sql
UNIQUE (campaign_id, user_id, channel)
-- ekleme: INSERT ... ON CONFLICT DO NOTHING
```
Kampanya iki kez başlatılsa bile ikinci teslimat satırı oluşmaz. Bu katman "iki kez sıraya koymayı" keser.

**Katman 2 — claim + fencing.**
```sql
UPDATE deliveries
SET status = 'sending', attempts = attempts + 1
WHERE id = $1 AND status IN ('queued', 'retrying');
-- etkilenen satır = 0 → başka bir worker aldı, biz boşuna geldik
```
Sonuç yazarken de `WHERE attempts = $aldığım_değer` koşulu koyuyoruz. Senaryo: worker A bir satırı aldı, sonucu yazamadan takıldı. Uzlaştırıcı "bu satır 5 dakikadır sending, ölmüş" deyip yeniden kuyruğa koydu; worker B aldı ve gönderdi. Sonra A canlanıp "gönderdim, 200 aldım" dese B'nin sonucunu ezer — ve satır iki kez sayılır. Fencing sayacı (attempts) tam bu: **A'nın elindeki bilet eski bilettir, sonuç yazamaz.**

**Katman 3 — sağlayıcıda idempotency.**
Her gönderim sabit başlıkla gider:
```
Idempotency-Key: scc-delivery-<teslimat-id>
```
Worker gönderdikten sonra, sonucu Postgres'e yazamadan ölürse ne olur? Yeniden deneme aynı teslimat id'siyle tekrar gider — sağlayıcı anahtarı tanır, **mesajı tekrar iletmez**, ilk cevabın aynısını döner. Bizde bu durum metrikte `sent_replayed` olarak görünür.

Dikkat: üçüncü katman olmadan 1+2 yeterli değil. "Redis'te gönderildi bayrağı" ya da outbox tablosu da aynı "gönderdim ama yazamadan öldüm" penceresini kapatamıyor. O katman varken de ek yapıya gerek kalmıyor — bu yüzden outbox bilinçli olarak yok.

**Testi de düşünelim:** `TestCampaignDeliversExactlyOnce` — hata enjeksiyonu açıkken (429, 5xx, zaman aşımı) **iki worker** aynı kampanyayı gönderir. Ölçüt sistem içi değil: sahte sağlayıcının kendi saydığı `duplicate_deliveries` değerinin 0 olması. Yani mükerrer yokluğunu sistem *dışından* doğruluyoruz.

Üç katman çok gibi görünse de maliyeti düşük: bir kısıt zaten bedava; fencing tek bir `WHERE` koşuluna sayaç eklemek; idempotency başlığı bir satır. Ekstra altyapı yok, outbox yok, ikinci kuyruk yok — basit tutmanın yolu katmanları atmak değil, her katmanın tek satır/tek koşul kalması. Öyle yazıyorum.

Ayrıca iptal de bu durum makinesine oturuyor: kampanya iptal edilince `queued`/`retrying` satırları `skipped` yapılır, `sending`'ler akışını tamamlar ama yenisi çıkmaz. Testi `TestCancelStopsPendingDeliveries` ile sınarız.

## User:
ok tamam speed limiti ya 100 sn basina kanal ve workerlar arasindan ortak bu nasil olucak

## Claude:
Anahtar gereklilik: sınır **süreçler arasında ortak**. Yani her worker'da lokal sayaç tutulamaz — 4 worker × 100/sn = sağlayıcıya 400/sn demek.

Seçenekler masaya yatıralım:

| Yönten | Sorun |
|---|---|
| Sabit pencere sayaç | Pencere sınırında **2 kat burst**: 11:59:59'da 100 + 12:00:01'de 100 = 200/sn anlık. Sağlayıcı sınırı 120/sn → 429 yağmuru. |
| Kayan pencere (ZSET) | Doğru ama her istekte O(log n) yazma + bellek; süreç sayısı arttıkça ZSET sıcak anahtar. |
| Token bucket | Uygun; ama bucket durumunu Redis'te atomik güncellemek yine Lua, karmaşıklığı GCRA ile aynı. |

Önerim: **GCRA** (Generic Cell Rate Algorithm). Tek bir anahtar (kanal başına), tek bir sayı: bir sonraki teorik varış zamanı. Lua betiği atomik çalışır:

- `now < tat` → çok erken, beklet/yeniden kuyruğa (429 gibi)
- değilse → `tat = max(now, tat) + 1/rate`, gönder

Farkı ne? GCRA gönderimleri `1/rate` aralığıyla **düzgün yayar**, burst izni hiç yok. Sabit pencerenin "pencere sınırı çift patlaması" sorunu kökten yok. Tek anahtar + sabit maliyet; worker sayısından bağımsız.

Test: `TestLimiterSpacesSendsAcrossWorkers` — birden çok worker'la toplam hızın 100/sn'yi aşmadığını ölçer. Canlı panoda da sınır çizgisiyle gerçek gönderim hızını yan yana koyarız ki davranış gözle görülsün.

## User:
redo retry filan nasil biliyosun ya provider 429 verse napcaz

## Claude:
Retry politikasını iki soruya bölelim: **neyi** yeniden deneriz, **ne zaman**.

**Neyi:**
- Geçici: **429, 5xx, zaman aşımı** → yeniden dene.
- Kalıcı: diğer 4xx (geçersiz numara, içerik reddi) → `failed`, denemek boşa. "Kalıcı hata" için 401/403/404'ü deneme; "429 belli ki geçici" ayrımı yapılıyor.

**Ne zaman — iki gerçek bilgi birleşiyor:**
1. Sağlayıcı 429'da `Retry-After` başlığı gönderiyorsa: o değeri **alt sınır** olarak al. Daha erken denemek boşa gider, üstelik limiti daha çok zorlar.
2. Basit üstel bekleme (`1s, 2s, 4s...`) tehlikeli: yüzlerce mesaj aynı anda düşüp **aynı anda** geri dönerse → senkron ikinci dalga → yeni 429/5xx. Bu klasik retry storm. Çare AWS'nin "full jitter" yaklaşımı:
   ```
   bekleme = rastgele(0, min(max, base · 2^(n-1)))
   ```
   Rastgelelik dalgalayıp ezber bozar.

**Mekanik:**
- Bekleyen satırlar `scc:retry` ZSET'ine park edilir (skor = deneme zamanı).
- 250 ms'de bir çalışan küçük bir Lua betiği zamanı gelenleri atomik olarak stream'e geri taşır. Ayrı süreç gerekmez, worker içinde.
- Üst sınır: **8 deneme.** Sonrasında `failed` — sonsuz deneme hem kaynak israfı hem "bu kampanya bitmedi" görünümü.

İptal etkileşimi de önemli: park edilmiş satırlar iptalde `skipped` olur, süre dolsa bile stream'e dönmez.

Bunlar `backend/internal/delivery/` altında: `worker.go`, `queue.go`, `ratelimit.go`, `backoff.go`, `store.go`.

## User:
ok simdi composer kismina donelim ya claude bana salak salak rule verirse napcaz mesela olmayan sehir filan

## Claude:
İyi soru — Structured Outputs biçimi garanti eder, **anlamı garanti etmez**. "Şehir: Şşşstanbul" şemaya uyar ve içeri geçer. Ya da SMS'i değişkenlerle doldurunca 172 karakter çıkar (limit 160).

O yüzden akış şu:

```
Claude (Structured Outputs: şema)  →  backend doğrulayıcı  →  hatalıysa modele geri  →  max 3 deneme
```

Doğrulayıcı **alan yoluyla** hata üretir:
```
variants[0].sms: 172 characters after rendering, max 160
rule.all[0].values[0]: unknown city "Şşşstanbul"
```
Bu hatalar bir sonraki denemede modele aynen geri verilir — onarım döngüsü. 3 deneme sonunda iki konu **farklı** ele alınır:

- **Kural:** geçersiz kural **asla kaydedilmez**. Hatalı kitleye mesaj gitmesindense hiç kural olmasi daha iyi; kural "neredeyse doğru" olamaz.
- **İçerik:** son deneme **taslak olarak kaydedilir**, hatalı alanlar düzenleyicide kırmızı işaretlenir. 3 karakter fazla diye SMS'i atıp pazarlamacıyı baştan başlatmak kötü UX; düzeltmesine izin ver. Onay adımı içeriği yeniden doğruladığı için hatalı içerik **gönderilemez** — kapalı kapı.

Bir incelik daha: SMS uzunluğu ham metin üzerinden değil, **değişkenler kitledeki en uzun değerlerle doldurularak** ölçülür. `{{first_name}}` şablonda 14 karakter tutar ama kitlede "Muhammed Mustafa" varsa o kullanıcının SMS'i patlar. En uzun değerle ölçmek garantili yöntem.

**Denetim izi:** her LLM çağrısı `llm_calls` tablosuna yazılır — prompt sürümü, model, deneme numarası, sonuç, hatalar, token, süre. "Bu kampanya neden o kitleye gitti?" sorusu sonradan da cevaplanabilmeli. Arayüzde kampanya bazında görünür.

**Prompt caching:** sistem prompt'u amaç başına sabit (kitle için ayrı, içerik için ayrı), bütün çağrılarda aynı — cache'lenir. Kampanyaya özgü veri (brief, katalog, önceki hatalar) kullanıcı mesajında gider. Maliyet ve gecikme düşer.

## User:
pano kismi nasil ya websocket mi napcaz canli gidecek

## Claude:
Kandırılmıyoruz: "canlı pano" için WebSocket refleksi var ama burada akış **tek yönlü** (sunucu → tarayıcı) ve 1 saniyelik çözünürlük yeter. Üç seçenek:

1. **Worker → pub/sub → pano olay yayını.** Ölçeklenir ama kayıptır: tarayıcı yeniden bağlanınca kaçırdığı olayları alamaz. Kayıpsız yapmak için olay günlüğü + doldurma mekanizması gerekir — karmaşıklık büyür.
2. **WebSocket.** Çift yönlü burada gereksiz; ek protokol, heartbeat, proxy konfigürasyonu — masraf, fayda yok.
3. **SSE + saniyede bir Postgres anlık görüntüsü.** API her saniye indeksli bir `GROUP BY` çekip SSE ile yollar.

3'ü seçiyorum:
- **Ölçeklenebilir:** herhangi bir API kopyası her kampanyayı sunabilir (yapışkan oturum yok, pub/sub bağımlılığı yok).
- **Kayıpsız:** her mesaj **tam durumu** taşır; yeniden bağlanan istemci kaçırdığı olayı zaten umursamaz — bir saniye sonra eksiksiz anlık görüntü gelir.
- **Ucuz:** kampanya başına indeksli `GROUP BY`, pano istemci sayısından bağımsız (tüm bağlantılar aynı anlık görüntüyü alır).

Bedeli: 1 sn çözünürlük. Bir pano için yeterli. Nginx'te tek satır: `proxy_buffering off`. Bu, panonun "hız sınırı çizgisi vs gerçek gönderim hızı" grafiğiyle birleşince demo'da güzel görünür.

## User:
hobey mock provider kismini de konusalim nasil bi sey olsun

## Claude:
Sahte sağlayıcı **ayrı bir süreç** olacak, gerçek HTTP sınırı olan `mockprovider` — README'de zaten "docker compose up her şeyi gösterir" dedik, kandirmamiz yok: gerçekçi hatalar da enjekte edecek:

- **Gecikme:** 20–250 ms
- **429 + `Retry-After`**
- **500**
- **Asılı kalma** (zaman aşımı senaryosu)
- **Kendi hız sınırı:** 120/sn (gerçek sınırın az üstü — 100/sn'lik GCRA'nın gerçekçi bir tavanla çalıştığını gösterir)

Ve kritik kısım: kendi tarafında **idempotency anahtarlarını sayar**, `duplicate_deliveries` sayacını `/stats`'ta (port 8090) açar. Yani exactly-once testimiz "içeriden kendini kandırmak" değil, sistem dışı ölçüm.

Oranları env ile ayarlanabilir yapıyorum: 0'a çekince sorunsuz bir sağlayıcı kalır — demo akışında hata enjekte etmek istemeyebilirsin; testlerde geri açarız.

## User:
bonuslari konusalim simdi hangileri yapiyoruz hangileri ypmiyoruz

## Claude:
Bonusları birer birer:

1. **İleri tarihli gönderim — YAPILIYOR.** `scheduled_at` alanı + zamanlayıcı. Sorun: birden çok worker aynı anda zamana gelen kampanyaları toplamasın → `SKIP LOCKED` ile satır satır kilitleyerek alır. `SELECT ... FOR UPDATE SKIP LOCKED` klasik doğru araç.

2. **Dile göre içerik — YAPILIYOR.** Kitledeki dillerden (`tr`, `en`) bağımsız varyant listesi; her kullanıcıya kendi dilinde varyant gider. İçerik zaten varyant listesi olduğundan model buna hazır.

3. **CI — YAPILIYOR.** GitHub Actions: `gofmt`, `vet`, `-race` testler (gerçek Postgres + Redis, `docker-compose.test.yml` ile geçici ayağa kalkar, bitince silinir), frontend `vue-tsc` + `vitest`, image build. Koddan çok testin savunması güçlü olur.

4. **A/B varyantları — YAPMIYORUZ ve bunun sebebi önemsiz değil.** Eksik olanlar sadece atama (`hash(user_id) mod n`) ve kol metrikleri değil; asıl eksik **kazanan seçme kuralı**. Tıklama/dönüşüm verisi mock sağlayıcıdan gelmez; veri olmadan "kazanan" uydurma olur. Veri modeli hazır (içerik zaten varyant listesi) — sonradan eklemek kolay, şimdi yapayım derken sahte bir metrik üretmek savunmada ters teper. Bu "yapılmadı + neden" hali DECISIONS.md'de açıkça durmalı.

**Yapılmayanlar listesi de gizlemesek iyi olur — bilinçli ödünler:**
- **Auth + çok kiracılık:** kapsam dışı; gerçek sistemde her sorgu `tenant_id` ile sınırlanır.
- **DLR (teslim raporları):** sağlayıcının "kabul ettim"i `sent` sayılıyor; gerçek SMS'te asıl teslimat webhook'la sonradan gelen durumdur.
- **Kampanya başına hız sınırı / öncelik:** sınır kanal başına ortak; iki büyük kampanya adil paylaşmaz, ilk gelen önde gider.
- **Opt-out + sessiz saatler:** gerçek pazarlama gönderiminde zorunlu (İYS, KVKK); burada modellenmedi — savunmada "neden yok?" sorusuna hazır cevap bu.

## User:
tamam kanka son bi tur at siteyi bi bakayim sonra testleri filan bitir

## Claude:
Tamamdır. Son durum:

- Arayüz 3000'de; brief → kitle → mesajlar → onay → pano akışının tamamı çalışıyor.
- `TestCampaignDeliversExactlyOnce`, `TestCancelStopsPendingDeliveries`, `TestLimiterSpacesSendsAcrossWorkers` entegrasyon testleri `make test` ile geçici Postgres/Redis'te koşuyor; frontend'de `vue-tsc` + `vitest`.
- DECISIONS.md'yi güncelledim: üç katmanlı mükerrer savunması, GCRA, full jitter + Retry-After, doğrula-onar döngüsü, SSE pano, sahte sağlayıcı, bonuslar ve bilinçli ödünler — her biri "karar + gerekçe + alternatif" formatında.

Görüşmede savunma gündemi bence şu sırayla hazır: yığın seçimi → veri akışı (Postgres tek gerçeklik) → exactly-once üç katman → GCRA → retry → DSL güvenliği. Her birinin "alternatif neydi, neden elendi" cevabı yazılı.

Kolay gelsin!

# Tasarım kararları

Her başlıkta önce kararı, sonra nedenini ve neyden vazgeçildiğini yazdım.

## Yığın: Go, Postgres, Redis, Vue

**Go.** Sistemin zor kısmı eşzamanlı gönderim: kanal başına onlarca tüketici, hız sınırı, yeniden deneme ve iptal. Go'da bunlar goroutine, `context` ve standart kütüphaneyle, çerçeve olmadan yazılabiliyor. Dört süreç (`api`, `worker`, `seed`, `mockprovider`) aynı statik binary'den çıkıyor, image küçük kalıyor. Anthropic'in resmî Go SDK'sı streaming ve Structured Outputs destekliyor.

**Postgres.** Kitle kuralları bir ilişkisel sorguya derleniyor (kullanıcı alanları ve "son N günde şu olay oldu/olmadı" alt sorguları). Teslimat durum makinesi de transaction ve satır kilidiyle en güvenli şekilde Postgres'te tutuluyor.

**Redis.** Yalnızca koordinasyon için kullanılıyor: kanal başına iş kuyruğu (Streams), yeniden deneme takvimi (ZSET) ve süreçler arası ortak hız sınırı (Lua).

**Vue 3 + Vite.** Arayüz tek sayfa ve durumu az; Vue'nun reaktivitesi stream edilen kısmi içeriği ekrana basmak için yeterli, ek bir durum kütüphanesi gerekmedi.

## Postgres tek doğruluk kaynağı, Redis yalnızca id taşır

Stream'lere yalnızca teslimat id'si yazılıyor. Mesaj içeriği, kullanıcı, deneme sayısı ve durum Postgres'te duruyor.

- **Redis kaybı veri kaybı değil.** Redis boşalırsa uzlaştırıcı, `queued` ya da `retrying` durumunda kalmış satırları Postgres'ten bulup yeniden kuyruğa koyuyor.
- **Pano ve worker aynı kaynağa bakıyor.** Kuyruk, panoyla ayrı bir durum tutmuyor.

Bedeli: her gönderimde bir claim ve bir sonuç yazımı, yani iki Postgres yazması. Saniyede kanal başına 100 mesajda bu yük önemsiz. Saniyede on binlere çıkılsaydı sonuçları toplu yazmak gerekirdi.

## Mükerrer gönderim yok: üç katmanlı savunma

"Exactly-once" ağ üzerinden garanti edilemez. Hedef, her (kampanya, kullanıcı, kanal) için tek bir mesajın sağlayıcıda *kabul edilmesi*. Bunu üç katman birlikte sağlıyor:

1. **`UNIQUE (campaign_id, user_id, channel)`.** Kampanya iki kez başlatılsa bile ikinci teslimat satırı oluşmaz. Ekleme `ON CONFLICT DO NOTHING` ile yapılıyor.
2. **Claim ve fencing.** Bir satırı `sending` durumuna yalnızca tek bir `UPDATE … WHERE status IN ('queued','retrying')` alabilir ve bu güncelleme `attempts` sayacını artırır. Sonuç yalnızca `attempts` hâlâ aynıysa yazılır. Böylece uzlaştırıcının "öldü" sandığı ama aslında yavaş çalışan bir worker, yeni denemenin sonucunu ezemez.
3. **Sağlayıcıda idempotency.** Her teslimat sabit bir `Idempotency-Key: scc-delivery-<id>` ile gönderiliyor. Worker gönderdikten sonra, sonucu yazamadan ölürse yeniden gönderim yeni mesaj üretmez; sağlayıcı ilk cevabı tekrar döner. Bu durum metriklerde `sent_replayed` olarak sayılıyor.

`TestCampaignDeliversExactlyOnce`, hata enjeksiyonu açıkken iki worker'la bunu sınıyor. Ölçüt, sahte sağlayıcının saydığı `duplicate_deliveries` değerinin 0 olması.

Alternatif olarak bir outbox ya da Redis'te "gönderildi" bayrağı düşünülebilirdi. Ancak üçüncü katman (sağlayıcı tarafı idempotency) olmadan hiçbiri "gönderdim ama yazamadan öldüm" penceresini kapatmıyor; o katman varken de ek bir yapıya gerek kalmıyor.

## Hız sınırı: Redis'te GCRA

Sınır saniyede kanal başına 100 mesaj ve birden çok worker süreci arasında ortak olmalı. Bu yüzden sınır, her süreçte ayrı tutulan bir sayaç olamazdı.

GCRA (Generic Cell Rate Algorithm) tek bir anahtarda bir sonraki teorik varış zamanını tutar ve Lua betiğiyle atomik çalışır. Gönderimleri `1/rate` aralığıyla düzgünce yayar, burst izni vermez.

Sabit pencereli sayaç pencere sınırında 2 kat burst'e izin verirdi. Sağlayıcının kendi sınırı da saniyede 120 olduğundan bu 429'a yol açardı. Kayan pencere ZSET'i her istekte O(log n) yazma ve bellek maliyeti getirirdi. GCRA tek anahtar ve sabit maliyetle daha düzgün bir akış veriyor.

`TestLimiterSpacesSendsAcrossWorkers` toplam hızı ölçüyor. Canlı panoda sınır çizgisi ve gerçek gönderim hızı yan yana görünüyor.

## Yeniden deneme: tam jitter ve Retry-After alt sınırı

- **Hangi hatalar yeniden denenir:** 429, 5xx ve zaman aşımı geçicidir; diğer 4xx hataları kalıcıdır ve satır `failed` olur.
- **Bekleme süresi:** `[0, min(max, base·2^(n-1))]` aralığından rastgele seçiliyor (AWS'nin "full jitter" yaklaşımı). Sağlayıcıdaki bir aksaklık yüzlerce mesajı aynı anda düşürdüğünde, yeniden denemelerin aynı anda gelip yeni bir aksaklığa yol açmasını bu rastgelelik engelliyor.
- **Retry-After:** Sağlayıcı bu başlığı gönderirse değeri alt sınır olarak kullanılıyor; daha erken denemek boşa giderdi.
- **Park ve geri taşıma:** Bekleyen satırlar `scc:retry` ZSET'inde duruyor. Lua betiği 250 ms'de bir zamanı gelenleri atomik olarak stream'e geri taşıyor.
- **Üst sınır:** En fazla 8 deneme yapılıyor.

## Kitle kuralı: LLM SQL yazmaz

Claude'dan SQL istemek en hızlı yol olurdu. Ama hem güvenlik (enjeksiyon, `DELETE`, yavaş sorgular) hem de kullanıcının kuralı anlayıp düzenleyebilmesi açısından kabul edilemezdi. Bunun yerine:

- **Küçük bir JSON DSL:** `all` / `any` birleştiricileri ve `text`, `number`, `event` koşulları.
- **Katalog:** Alanlar, operatörler, izinli değerler ve sınırlar (`within_days ≤ 365` gibi) tek yerde tanımlı. Hem prompt'a hem doğrulayıcıya bu katalog giriyor; prompt ile doğrulama birbirinden kopamıyor.
- **Derleyici:** Yalnızca sabit SQL parçalarını birleştiriyor, değerleri `$n` parametresi olarak geçiyor.

Aynı kural arayüzde form olarak düzenlenebiliyor. Pazarlamacı, Claude'un yanlış anladığı bir şeyi elle düzeltip kitle sayısının canlı değiştiğini görebiliyor.

DSL bilinçli olarak sınırlı. "Son 48 saatte erken erişim" gibi katalogda olmayan bir istek sessizce yaklaşık bir kurala çevrilmiyor; Claude bunu `unsupported` notu olarak dönüyor ve arayüz gösteriyor.

## Yapılandırılmış çıktı ve doğrula-onar döngüsü

Claude'a JSON şeması Structured Outputs (`output_config`) ile veriliyor. Bu biçimi garanti ediyor, anlamı garanti etmiyor: var olmayan bir şehir ya da 172 karakterlik bir SMS hâlâ gelebilir.

Bu yüzden her çıktı backend'de alan yoluyla hata üreten bir doğrulayıcıdan geçiyor. Hatalar bir sonraki denemede modele aynen geri veriliyor. Örnek: `variants[0].sms: 172 characters after rendering, max 160`.

Döngü en fazla 3 deneme sürüyor. Sonrasında iki konu farklı ele alınıyor:

- **Kural:** Geçersiz bir kural asla kaydedilmez. Hatalı bir kitleye mesaj gitmesindense hiç kural olmaması daha iyi.
- **İçerik:** Son deneme taslak olarak kaydedilir ve hatalı alanlar düzenleyicide işaretlenir. 3 karakter fazla bir SMS'i atıp pazarlamacıyı baştan başlatmak yerine düzeltmesine izin vermek daha kullanışlı. Onay adımı içeriği yeniden doğruladığı için hatalı içerik gönderilemiyor.

SMS uzunluğu ham metin üzerinden değil, değişkenler kitledeki *en uzun* değerlerle doldurularak ölçülüyor. `{{first_name}}` 14 karakter tutar, ama "Muhammed Mustafa" daha uzundur.

**Denetim izi.** Her çağrı `llm_calls` tablosuna yazılıyor: prompt sürümü, model, deneme numarası, sonuç, hatalar, token sayısı ve süre. Kampanya bazında arayüzde de görünüyor. Bir kampanyanın neden o kitleye gittiği sonradan açıklanabilmeli.

**Prompt caching.** Her amacın (kitle, içerik) sistem prompt'u bütün çağrılarda aynı olduğundan cache'leniyor. Kampanyaya özgü veri (brief, katalog, önceki hatalar) kullanıcı mesajında gidiyor.

## LLM katmanı ve demo modu

Composer, `llm.Client` adlı küçük bir arayüze bağlı. Bu arayüzün üç gerçekleştirmesi var:

| Gerçekleştirme | Ne zaman | Ne yapar |
|---|---|---|
| `anthropic.go` | Anahtar varsa | Gerçek Claude, streaming |
| `demo/` | Anahtar yoksa | Anahtar kelime sezgileri, sahte stream |
| `Scripted` | Testlerde | Sırayla hazır cevaplar |

Değerlendirenin elinde anahtar olmayabilir, ya da ortamı internetsiz olabilir. `docker compose up` yine de akışın tamamını göstermeli. Demo modu brief'teki örnek kampanyaları makul kurallara çeviriyor, ama gerçek bir model gibi davranmıyor; arayüz bunu açıkça belirtiyor.

Testler `Scripted` ile deterministik çalışıyor. Geçersiz JSON, şema dışı kural ve onarım sonrası başarı gibi senaryolar ağa çıkmadan sınanıyor.

Arayüz başka sağlayıcıların eklenmesine izin veriyor (bir OpenAI uyumlu istemci düşünüldü), ancak kapsam dışında bırakıldı. Projede yalnızca Claude kullanılıyor.

## İzin kontrolü iki kez yapılıyor

1. **Teslimatlar oluşturulurken:** İzni olmayan kullanıcıya satır açılmıyor.
2. **Gönderimden hemen önce:** Zamanlanmış bir kampanyada onay ile gönderim arasında saatler geçebilir. Bu arada izni geri çeken kullanıcının satırı `skipped` oluyor.

Kitle de onay anında değil, kampanya başladığında yeniden hesaplanıyor. Onay ekranındaki sayı o anki tahmin; gerçek sayı başlangıçta belli oluyor.

## Canlı pano: SSE ve Postgres'i yoklama

Worker'dan pub/sub ile olay yaymak yerine API her saniye Postgres'ten bir anlık görüntü alıp SSE ile gönderiyor.

- **Ölçeklenebilir:** Herhangi bir API kopyası her kampanyayı sunabiliyor.
- **Kayıpsız:** Yeniden bağlanan istemci olay kaçırmıyor, çünkü her mesaj tam durumu taşıyor.
- **Ucuz:** Sorgu, kampanya başına indeksli bir `GROUP BY`.

Bedeli: güncellemeler 1 saniyelik çözünürlükte geliyor. Bir pano için bu yeterli.

WebSocket'e gerek yok, çünkü akış tek yönlü. SSE'nin nginx üzerinden çalışması için `proxy_buffering off` yeterli oluyor.

## Sahte sağlayıcı

Sahte sağlayıcı ayrı bir süreç olarak çalışıyor, gerçek bir HTTP sınırı var. Şunları enjekte ediyor:

- Gecikme (20–250 ms).
- `Retry-After` başlıklı 429.
- 500.
- Yanıt vermeden asılı kalma (zaman aşımı).
- Kendi hız sınırı (saniyede 120).

Kendi tarafında idempotency anahtarlarını tutup `duplicate_deliveries` sayıyor. Bu, mükerrer gönderim olmadığının sistem dışından doğrulanması anlamına geliyor. Oranlar env ile değiştirilebiliyor; 0'a çekilince sorunsuz bir sağlayıcı elde ediliyor.

## Bonuslar

| Bonus | Durum |
|---|---|
| İleri tarihli gönderim | Yapıldı: `scheduled_at`. Zamanlayıcı, birden çok worker'da güvenli çalışsın diye `SKIP LOCKED` kullanıyor. |
| Dile göre içerik | Yapıldı: Kitledeki diller (`tr`, `en`) için ayrı varyantlar üretiliyor ve her kullanıcıya kendi dilindeki varyant gidiyor. |
| CI | Yapıldı: GitHub Actions'ta gofmt, vet, race'li testler (gerçek Postgres ve Redis ile), frontend tip kontrolü, testler ve image derlemesi. |
| A/B varyantları | Yapılmadı (aşağıda). |

## Yapılmayanlar ve bilinçli ödünler

- **A/B varyantları:** Veri modeli buna hazır, çünkü içerik zaten bir varyant listesi. Eksik olanlar:
  - Kullanıcıyı deterministik olarak bir kola atamak, örneğin `hash(user_id) mod n`.
  - Kol başına metrik.
  - Bir kazanan seçme kuralı.

  Bu sonuncusu, tıklama ya da dönüşüm verisi olmadan anlamlı olmazdı; mock sağlayıcı bu veriyi üretmiyor.
- **Kimlik doğrulama ve çok kiracılık:** Kapsam dışı. Gerçek bir sistemde her sorgu bir `tenant_id` ile sınırlanırdı.
- **Teslim raporları (DLR):** Sağlayıcının "kabul ettim" demesi `sent` sayılıyor. Gerçek bir SMS sağlayıcısında asıl teslimat, sonradan webhook ile gelen bir durumdur.
- **Kampanya başına hız sınırı ve öncelik:** Sınır kanal başına ortak. Aynı anda iki büyük kampanya çalışırsa bant genişliğini adilce paylaşmıyorlar; ilk kuyruğa giren önde gidiyor.
- **Opt-out bağlantısı ve sessiz saatler:** Gerçek bir pazarlama gönderiminde zorunlu (İYS, KVKK), ancak burada modellenmedi.

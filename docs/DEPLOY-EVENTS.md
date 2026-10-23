# Deploy event'leri — pipeline'dan deploy kaydı (v0.9.1204)

Coremetry deploy'ları normalde telemetriden çıkarır: bir servisin daha
önce görülmemiş bir versiyonu ilk span'ini attığında bu bir deploy
sayılır. Versiyon şu attribute zincirinden okunur:
`container.image.tag` → `k8s.container.image.tag` → `service.version` →
k8s/helm version label'ları.

**Bu zincirin hiçbir halkası değişmiyorsa** (tipik örnek: uzun ömürlü
JBoss'a WAR deploy'u — süreç, container ve attribute'lar aynı kalır)
deploy görünmez ve önce/sonra etki analizi ile kök-neden "ne değişti"
korelasyonu kör kalır.

Çözüm: release pipeline'ınızın son adımı deploy'u KENDİSİ bildirir.

## Azure DevOps (TFS) release pipeline adımı

```bash
curl -sS -X POST "$COREMETRY_URL/api/operator-events" \
  -H "Authorization: Bearer $COREMETRY_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "kind": "deploy",
    "service": "shop-cashpayment-wire-prod",
    "label": "release-2026.08.21.3",
    "link": "'"$RELEASE_RELEASEWEBURL"'"
  }'
```

- `service` — Coremetry'deki servis adıyla AYNI olmalı (`service.name`).
- `label` — versiyonun kendisi sayılır (parse edilmez); Azure DevOps'ta
  `$(Release.ReleaseName)` ya da `$(Build.BuildNumber)` doğal adaydır.
- `time` — verilmezse "şimdi"; geriye dönük kayıt için unix nanosaniye
  gönderilebilir.
- `link` — isteğe bağlı; ya mutlak bir `http(s)://` URL'si (ör. release
  sayfası) ya da `/` ile başlayan bir Coremetry uygulama yolu olmalı.
  Başka her şey (`javascript:`, `data:`, `//host`, kullanıcı:parola'lı URL)
  `400` döner. Bu kuraldan önce saklanmış geçersiz linkler okumada düşer
  (olay görünür, link görünmez) — v0.10.1315.
- Token: Settings → API Tokens'tan editor rollü bir token yeterli.

## Ne kazanırsınız

Event kaydı, span-çıkarımlı deploy'larla AYNI kaynak muamelesi görür:

- "What changed" banner'ı ve grafiklerdeki deploy marker'ları,
- /deploys geçmişi (satırda `pipeline` rozetiyle) ve **önce/sonra RED
  etki analizi** (p99/hata/rps kıyası — deploy ANI bilinince attribute
  gerekmez),
- problem satırlarındaki "recent deploy" korelasyonu ve kök-neden
  hipotezindeki deploy kanıtı.

Aynı (servis, versiyon) hem telemetriden çıkarılır hem event'ten
gelirse **event kazanır** — pipeline kaydı gerçeğin kendisidir.

## Argo CD senkron webhook'u — geri almanın git kaynağı (v0.10.1445)

KSM dedektörü `kubectl rollout undo` ile GitOps revert'ini (ya da eski
imajı pinleyen ileri bir commit'i) ayıramaz: pod şablonu bayt bayt aynı
olunca Kubernetes eski ReplicaSet'i yeniden kullanır. Argo CD metrikleri
revizyon taşımaz. Ayrımı Argo CD Notifications'ın her senkron bitişinde
Coremetry'ye gönderdiği kayıt yapar → `/rollouts` "change" hücresinde
ikinci rozet: **Rollback (git)** (Argo daha önce senkronlanmış bir
revizyona döndü) ya da **Redeploy (older)** (yeni git revizyonu, eski
yapı). Kurulmayan ortamda rozet yoktur; KSM "Rollback" türü aynen kalır.

Ön koşullar (Coremetry tarafı):

- Ayarlar › Argo CD: entegrasyon açık, instance **kayıtlı ve etkin**;
  webhook gövdesindeki `instanceId` = `instances[].id` (yoksa 400).
- argocd-metrics işçisi açık (iş yükü ↔ Application eşlemesi,
  `argocd_app_mapping`, ondan gelir; eşleme yoksa satır `unknown`).
- **editor** rolünde bir Coremetry API token'ı.

Argo CD tarafı (`argocd` namespace'i; Helm chart kullanıyorsanız aynı
anahtarlar `notifications.{notifiers,templates,triggers,subscriptions}`
altında). Token `argocd-notifications-secret` içinde `coremetry-token`:

```yaml
# argocd-notifications-cm
service.webhook.coremetry: |
  url: https://coremetry.example.invalid/api/argocd/sync-events
  headers:
  - name: Authorization
    value: Bearer $coremetry-token
  - name: Content-Type
    value: application/json
template.coremetry-sync: |
  webhook:
    coremetry:
      method: POST
      body: |
        {"instanceId":"argo-main",
         "app":{"namespace":"{{.app.metadata.namespace}}","name":"{{.app.metadata.name}}"},
         "operation":{"phase":"{{.app.status.operationState.phase}}","startedAt":"{{.app.status.operationState.startedAt}}","finishedAt":"{{.app.status.operationState.finishedAt}}","automated":{{if .app.status.operationState.operation.initiatedBy.automated}}true{{else}}false{{end}},"initiator":"{{.app.status.operationState.operation.initiatedBy.username}}"},
         "sync":{"revision":"{{.app.status.operationState.syncResult.revision}}","repoURL":"{{.app.spec.source.repoURL}}","targetRevision":"{{.app.spec.source.targetRevision}}"},
         "history":[{{range $i,$h := .app.status.history}}{{if $i}},{{end}}{"id":{{$h.id}},"revision":"{{$h.revision}}","deployStartedAt":"{{$h.deployStartedAt}}","deployedAt":"{{$h.deployedAt}}"}{{end}}],
         "resources":[{{range $i,$r := .app.status.operationState.syncResult.resources}}{{if $i}},{{end}}"{{$r.kind}}/{{$r.namespace}}/{{$r.name}}"{{end}}],
         "managed":[{{range $i,$r := .app.status.resources}}{{if $i}},{{end}}"{{$r.kind}}/{{$r.namespace}}/{{$r.name}}"{{end}}],
         "destination":{"server":"{{.app.spec.destination.server}}","namespace":"{{.app.spec.destination.namespace}}"}}
trigger.on-sync-finished: |
  - when: app.status.operationState.phase in ['Succeeded','Failed','Error']
    send: [coremetry-sync]
# Tüm uygulamalar için (ya da tek tek: Application'a
# notifications.argoproj.io/subscribe.on-sync-finished.coremetry: "" anotasyonu)
subscriptions: |
  - recipients: [coremetry]
    triggers: [on-sync-finished]
```

Davranış: gövde ≤ 256 KiB; `history[]` (Argo varsayılan 10 girdi) tohum
satırı olarak yazılır → ilk webhook'tan itibaren "bu revizyon daha önce
senkronlandı mı" cevaplanır. Tekrar gönderim zararsızdır
(ReplacingMergeTree). Zaman biçimi RFC3339 ya da Go `time.Time` metni.
Doğrulama: `curl -H "Authorization: Bearer $TOKEN" -X POST
https://coremetry.example.invalid/api/argocd/sync-events -d @sample.json`
→ `{"written":N}`; Admin › Audit'te `argocd.sync_event.create`.
Eşleşme penceresi: KSM `started_at` ∈ [senkron başlangıcı − W, bitiş + W],
W = Ayarlar › Argo CD › `classification.windowMin` (5 dk).

Geri alma: aboneliği `argocd-notifications-cm`'den kaldırın; yazılmış
satırlar TTL ile düşer.

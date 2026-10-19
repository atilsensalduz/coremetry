import { useEffect, useState } from 'react';
import { useBranding } from './branding';
import { getRaw, setRaw, removeRaw } from './storage';

// Tiny in-repo i18n. No external dependency — the SPA's
// branding settings carry a `language` field that picks one of
// the catalogs below. English is the source of truth + the
// default; Turkish covers the high-traffic surfaces (sidebar,
// login, common buttons, page titles, empty/error states).
//
// Catalog covers ~80% of what an operator sees during morning
// triage. Strings that aren't in the catalog fall back to the
// English key verbatim so a missing translation never renders
// blank.
export type Lang = 'en' | 'tr';

type Catalog = Record<string, string>;

const EN: Catalog = {
  // Sidebar group headings
  'navGroup.triage':      'Triage',
  'navGroup.services':    'Services',
  'navGroup.signals':     'Signals',
  'navGroup.workspaces':  'Workspaces',
  'navGroup.alerting':    'Alerting',
  'navGroup.system':      'System',
  'navGroup.management':  'Management',
  'navGroup.community':   'Community',
  // v0.10.1340 — kabuk atlama bağlantısı (AppShell SkipToContent)
  'shell.skipToContent': 'Skip to content',
  // v0.10.1340 END

  // Sidebar
  // v0.9.323 — triage merge. The merged queue takes the name operators
  // already use for "the thing I work from"; the per-source pages it
  // aggregates are drill-downs now, not competing queues. 'nav.inbox' keeps
  // its key (the route is unchanged) so saved views, notification deep links
  // and dashboard markdown all keep working — only the LABEL moved.
  'nav.inbox':       'Problems',
  'nav.shift':       'Shift summary',
  'nav.incidents':   'Incidents',
  'nav.problems':    'Exceptions/Errors', // v0.10.1107 (operatör) — Oracle hata grupları da burada; menüde iki ad
  'nav.anomalies':   'Anomalies',
  'nav.rollouts':    'Deployment/Rollouts', // v0.10.201 — Deployment Report → Rollouts; v0.10.984 (Rollouts v2 P2.3) — brief adı
  'nav.analysis':    'Analysis',
  'nav.services':    'Services',
  'nav.endpoints':   'Endpoints',
  'nav.databases':   'Databases',
  'nav.clusters':    'Clusters',
  'nav.messaging':   'Messaging',
  'nav.external':    'External APIs',
  'nav.hosts':       'Hosts',
  'nav.traces':      'Traces',
  'nav.metrics':     'Metrics',
  'nav.logs':        'Logs',
  // v0.10.1335 — birleşik arama sayfası (/search). {q}/{n}/{src}/{reason}
  // yer tutucuları çağıranda doldurulur (pages/search/searchText.ts).
  'nav.search':      'Search',
  'search.placeholder':   'Search services, endpoints, problems, exceptions, traces, logs…',
  'search.minChars':      'Type at least 2 characters',
  'search.minCharsBody':  'One query searches services, endpoints, problems, exceptions, traces and logs in the selected time range.',
  'search.loading':       'Searching…',
  'search.error':         'Search failed',
  'search.noMatch':       'No matches for “{q}”',
  'search.noMatchBody':   'Try a shorter query, a wider time range, or paste a trace id.',
  'search.hidden':        'No results with current filters',
  'search.hiddenN':       '{n} hidden',
  'search.clearAll':      'Clear all',
  'search.results':       '{n} results',
  'search.sort':          'Sort',
  'search.sort.relevance': 'Relevance',
  'search.sort.newest':   'Newest',
  'search.sort.oldest':   'Oldest',
  'search.sort.severity': 'Severity',
  'search.group':         'Group',
  'search.group.list':    'List',
  'search.group.type':    'By type',
  'search.facet.type':    'Type',
  'search.facet.service': 'Service',
  'search.facet.env':     'Environment',
  'search.facet.sev':     'Severity ≥',
  'search.facet.status':  'Problems',
  'search.facet.scope':   'Service, environment and severity counts: within shown results',
  'search.status.open':   'Open',
  'search.status.all':    'All',
  'search.type.service':  'Service',
  'search.type.endpoint': 'Endpoint',
  'search.type.problem':  'Problem',
  'search.type.exception': 'Exception',
  'search.type.trace':    'Trace',
  'search.type.log':      'Log',
  'search.src.unavailable': '{src} unavailable ({reason})',
  'search.src.needsService': '{src}: needs a service or trace id',
  'search.src.skipped':   '{src} skipped ({reason})',
  'search.open.trace':    'Open in Traces',
  'search.open.log':      'Open in Logs',
  'search.open.problem':  'Open in Problems',
  'search.open.exception': 'Open in Exceptions',
  'search.open.service':  'Open in Service',
  'search.open.endpoint': 'Open operation traces',
  'search.askCosre':      'Ask CoSRE',
  'search.seeAll':        'See all results for “{q}” →',
  'nav.explore':     'Explore',
  'nav.runbooks':    'Runbooks',
  'nav.dashboards':  'Dashboards',
  'nav.profiling':   'Profiling',
  'nav.ai':          'AI insights',
  'nav.alerts':      'Alerts',
  'nav.watchers':    'Watchers',
  'nav.serviceMap':  'Service map',
  'nav.topology':    'Topology',
  'nav.clickhouse':  'ClickHouse',
  'nav.elastic':     'Elasticsearch',
  'nav.slos':        'SLOs',
  'nav.monitors':    'Monitors',
  'nav.events':      'Events',
  'nav.system':      'System',
  'nav.cardinality': 'Cardinality',
  'nav.cluster':     'Cluster',
  'nav.catalog':     'Service catalog',
  'nav.audit':       'Audit log',
  'nav.sql':         'SQL playground',
  'nav.query':       'Query (DQL)',
  'nav.statusPage':  'Public Status Page',

  // Login
  'login.signIn':      'Sign in',
  'login.signingIn':   'Signing in…',
  'login.email':       'Email',
  'login.password':    'Password',
  'login.usernameOrEmail': 'Username or email',
  'login.signInWith':  'Sign in with',
  'login.orLocal':     'or sign in locally',
  'login.signInToContinue': 'Sign in to continue',
  'login.invalid':     'Invalid username or password',
  'login.invalidHint': 'If you pasted from a document, a hyphen-minus (-) may have been replaced with a similar character (–, —, hidden whitespace). Try typing the password by hand.',

  // Common buttons
  'btn.save':    'Save',
  'btn.cancel':  'Cancel',
  'btn.delete':  'Delete',
  'btn.reset':   'Reset',
  'btn.create':  'Create',
  'btn.edit':    'Edit',
  'btn.close':   'Close',
  'btn.refresh': 'Refresh',
  'btn.search':  'Search',

  // Status / states
  'state.loading':       'Loading…',
  'state.noData':        'No data',
  'state.failed':        'Failed to load',
  'state.tryWidening':   'Try widening the time range.',

  // User menu
  'user.signOut':         'Sign out',
  'user.changePassword':  'Change password',
  'user.manageUsers':     'Manage users',
  'user.settings':        'Settings',

  // Common labels
  'label.service':    'Service',
  'label.severity':   'Severity',
  'label.status':     'Status',
  'label.started':    'Started',
  'label.duration':   'Duration',
  'label.errorRate':  'Error rate',
  'label.latency':    'Latency',
  'label.count':      'Count',
  'label.value':      'Value',
  'label.threshold':  'Threshold',

  // Severity badges
  'sev.critical': 'CRITICAL',
  'sev.warning':  'WARNING',
  'sev.info':     'INFO',
  'sev.open':     'OPEN',
  'sev.resolved': 'RESOLVED',

  // Time range picker (Grafana-parity panel)
  'trp.quickRanges':   'Quick ranges',
  'trp.absoluteRange': 'Absolute time range',
  'trp.from':          'From',
  'trp.to':            'To',
  'trp.applyRange':    'Apply time range',
  'trp.recentRanges':  'Recently used',
  'trp.browserTime':   'Browser time',
  'trp.zoomOut':       'Zoom out (2×)',
  'trp.prevMonth':     'Previous month',
  'trp.nextMonth':     'Next month',
  'trp.errFrom':       'Invalid "From" — use YYYY-MM-DD HH:mm:ss',
  'trp.errTo':         'Invalid "To" — use YYYY-MM-DD HH:mm:ss',
  'trp.errOrder':      '"To" must be after "From"',
  'trp.errMax':        'Range too large (max 1 year)',

  // Quick-range labels (keys mirror PRESET_SECONDS in lib/utils.ts)
  'range.5m':  'Last 5 minutes',
  'range.15m': 'Last 15 minutes',
  'range.30m': 'Last 30 minutes',
  'range.1h':  'Last 1 hour',
  'range.3h':  'Last 3 hours',
  'range.6h':  'Last 6 hours',
  'range.12h': 'Last 12 hours',
  'range.24h': 'Last 24 hours',
  'range.2d':  'Last 2 days',
  'range.7d':  'Last 7 days',
  'range.30d': 'Last 30 days',

  // v0.10.944 (operatör) — trace sayfasının ✨ Explain düğmesi "CoSRE'ye sor"
  // adını aldı. Görünen ad + ipucu yalnız ARAYÜZ metni: ?ai=trace:<id>,
  // explain-trace yüzeyi ve AIExplainButton aynı kalır.
  'ai.askCosre':          'Ask CoSRE',
  'ai.askCosreTraceHint': 'Ask CoSRE about this trace.',
  // v0.10.1338 — span düğmesi Trace sayfasında trace düğmesiyle AYNI adı
  // taşır; ikisini ipucu ayırır.
  'ai.askCosreSpanHint':  'Ask CoSRE · span — explain this span.',
  // v0.10.1338 (operatör) — log satırı / log artışı düğmeleri de "Ask CoSRE"; ipucu ayırır.
  'ai.askCosreLogHint':      'Ask CoSRE · log — explain this log line.',
  'ai.askCosreLogSpikeHint': 'Ask CoSRE · log spike — explain this log increase.',
  // v0.10.1367 — Endpoint detay / arama endpoint sonucu: tek tık "Ask CoSRE".
  'ai.askCosreEndpointHint': 'Ask CoSRE · endpoint — what is happening, since when, where the time goes, which errors.',
  // v0.10.948 (CoSRE Faz B) — çekmece başlığının trace alt satırı: "Ask CoSRE · trace <kısa kimlik>".
  'ai.subject.trace':     'trace',

  // v0.10.944 (CoSRE Faz A) — çekmecenin "Bağlam" şeridi: sohbetin hangi
  // trace/span/servis/ortam/cluster/namespace ve pencereye kapsandığı.
  'ai.ctx.label':     'Context',
  'ai.ctx.aria':      'CoSRE conversation context',
  'ai.ctx.trace':     'Trace',
  'ai.ctx.span':      'Span',
  'ai.ctx.service':   'Service',
  'ai.ctx.env':       'Env',
  'ai.ctx.clusterNs': 'Cluster / namespace',
  'ai.ctx.window':    'Window',
  'ai.ctx.saved':     'Saved with the conversation — the live page may differ.',

  // v0.10.1128 — empty CoSRE chat: "what I can do" hint (drawer + /cosre).
  // `|` in a prompt marks where the caret lands (the user types the name).
  'cosre.cap.aria':             'What CoSRE can do',
  'cosre.cap.tryHint':          'Fills the box with an example — edit it, then send',
  'cosre.cap.wiki':             'I can search the company wiki — e.g. “wiki: how do I refresh the cache”',
  'cosre.cap.wiki.prompt':      'wiki: how do I refresh the cache',
  'cosre.cap.service':          'I can look at a service’s traces, errors and latency — type the service name (@ completes it)',
  'cosre.cap.service.prompt':   '@| traces, errors and latency in the last hour?',
  'cosre.cap.operation':        'I can dig into failing technical operations — type the operation name',
  'cosre.cap.operation.prompt': 'Why is the | operation failing?',
  'cosre.cap.navigate':         'I can tell you where to look and take you to the right page',
  'cosre.cap.navigate.prompt':  'Where should I look to investigate |?',
  'cosre.chip.wiki':            'Search the wiki: …',
  'cosre.chip.wiki.prompt':     'wiki: ',

  // v0.10.1119 — kök-neden paneli: ayrı bubbleUp isteği düştü (≠ "ayrışma yok").
  'rootCause.bubbleUnavailable': 'Attribute comparison unavailable — the span scan failed or timed out. This does not mean no dimension stood out.',

  // v0.10.1120 — Settings > SSO: trust unverified email (email_verified=false).
  'sso.trustEmail.label':        'Trust unverified email (not recommended)',
  'sso.trustEmail.warning':      'Logins with email_verified=false are accepted, but only from the allowed domains. Risk: account takeover through an email collision — use only if the corporate IdP does not let users set or change their own email (no self-registration, no email editing, no social/brokered IdPs) and allowed domains are set. Admin and local/LDAP accounts are never linked this way. Preferred fix is on the IdP side (Keycloak "Trust Email").',
  'sso.trustEmail.needsDomains': 'Set allowed domains first — unverified email cannot be trusted while the list is empty.',

  // v0.10.1121 — Settings > SSO: match by username when the IdP sends no email.
  'sso.usernameFallback.label':   'Match by username when email is missing (AD/LDAP)',
  'sso.usernameFallback.on':      'On: if neither the id_token nor UserInfo carries an email, the username claim from the verified id_token is looked up in AD/LDAP (exact match) and the directory email is used; without LDAP (or no email in the directory) the user is matched to an existing Coremetry user by LDAP username. No new user is created without an email. Allowed domains still apply.',
  'sso.usernameFallback.off':     'Off (default): if neither the id_token nor UserInfo carries an email, the login is rejected (email_missing). UserInfo is always tried. Preferred fix on the IdP: Keycloak LDAP federation mapper mail → email, and the email client scope as Default with "Add to ID token".',
  'sso.usernameFallback.risk':    'Use only if users cannot change this claim on the IdP (LDAP federation read-only, username editing off). Unsafe with brokered (external/social) IdPs, self-registration or an editable username — anyone who can pick the username can open that account. Admin accounts are not opened this way unless explicitly allowed below.',
  'sso.usernameAdmin.label':      'Also allow admin accounts via username matching (not recommended)',
  'sso.usernameAdmin.warning':    'An admin account becomes reachable by whoever controls this username on the IdP. Leave off; admins can sign in with password/LDAP.',
  'sso.usernameClaim.label':      'Username claim',
  'sso.usernameClaim.hint':       'Only this claim is read (default preferred_username = AD sAMAccountName). Letters, digits and _ - . : ; profile/email claims are not allowed.',

  // v0.10.1334 — in-app guided tour (features/tour), release R1: chrome,
  // steps 1–2, declared-but-disabled steps 3–5, notes.
  'tour.chrome.dialog':     'Guided tour',
  'tour.chrome.stepOf':     'Step {n} of {total}',
  'tour.chrome.progress':   'Tour progress',
  'tour.chrome.back':       'Back',
  'tour.chrome.next':       'Next',
  'tour.chrome.finish':     'Finish',
  'tour.chrome.skip':       'Skip tour',
  'tour.chrome.restart':    'Restart tour',
  'tour.chrome.loading':    'Looking for this part of the page…',
  'tour.note.timeout':      'No data yet — this part of the page has not rendered. You can still continue.',
  'tour.note.not_yet':      'Coming in the next release.',
  'tour.note.no_services':  'No services reporting yet — the tour becomes meaningful once OTLP data arrives.',
  'tour.welcome.title':     'Welcome — a two-minute tour',
  'tour.welcome.body':      'This is what this installation sees right now, over the last 5 minutes. Next we walk through the Services list, where most investigations start.',
  'tour.welcome.unavailable': 'Live numbers could not be loaded — the tour still works.',
  'tour.welcome.tile.services': 'Services',
  'tour.welcome.tile.spans':    'Spans · 5 min',
  'tour.welcome.tile.spansTop': 'Spans · 5 min · top 50',
  'tour.welcome.tile.busiest':  'Busiest service',
  'tour.welcome.tile.envs':     'Environments',
  'tour.services.title':    'Services: your starting point',
  'tour.services.body':     'Each row is one service sending OTLP spans. The dot before the name is its health: amber when the error rate is above 1 % (or a warning/info problem is open), red above 5 % (or a critical problem is open). Spans, Error rate and P99 cover the selected time range; turn on "Δ prior" to compare with the previous window. Click a row to open the service.',
  // v0.10.1336 — guided tour R2: steps 3–5 (operations / traces / cosre) + sub-steps.
  'tour.operations.title':  'Operations: where the time goes',
  'tour.operations.header': 'This is the page of the busiest service. The breadcrumb above the name goes back to Services with the same time window, ☆ pins the service to Cmd-K quick access, and these buttons jump to its backtrace, logs, endpoints, problems, anomalies and traces — the service and the window come along.',
  'tour.operations.tab':    'The Operations tab lists every operation (span name) of this service. The tab lives in the URL (?tab=operations), so a shared link opens right here.',
  'tour.operations.impact': 'Rows are ranked by Impact = average duration × call count: the operation that costs your users the most total time comes first — not merely the slowest one. Raw shows span names as they arrive; Normalized (top right) collapses id-bearing names into shapes such as GET /users/:id.',
  'tour.operations.body':   'Each row is one operation with its trend, calls, error rate and latency percentiles. Click a row to open that operation\'s traces — exactly where the next step goes.',
  'tour.traces.title':      'Traces: the slow and failing requests',
  'tour.traces.filters':    'We opened this service\'s traces with the status_code = error quick chip applied. Active filters sit in this bar (click one to edit, ✕ removes it); the quick row below ("Hızlı:") adds the common ones — errors, server spans, POST — in one click.',
  'tour.traces.minms':      'Min ms = 500 hides fast requests, so only slow and failing traces remain. The service picker on the left scopes the list; after changing a field press Search (or Enter).',
  'tour.traces.body':       'Each row is one trace — a single request across services. Click a row to open its waterfall: every span, its timing and its attributes.',
  'tour.cosre.title':       'CoSRE: ask in plain language',
  'tour.cosre.fab':         'This button opens CoSRE, the assistant that reads your telemetry. It knows the page you are on — on a service or a trace it starts from that context. A red badge counts open critical problems.',
  'tour.cosre.body':        'We drafted a question about this service — nothing has been sent. Press Finish, edit it if you like, then send it with Enter. Esc closes the chat drawer first; a second Esc skips the tour.',
  'tour.cosre.prompt':      'Why is {svc} slow?',
  'tour.cosre.promptFleet': 'What should I look at first right now?',
  'tour.note.copilot_disabled': 'CoSRE is turned off on this installation — an admin enables it in Settings › CoSRE.',
  // v0.10.1336 END
};

const TR: Catalog = {
  // Sidebar group headings
  'navGroup.triage':     'Olay yönetimi',
  'navGroup.services':   'Servisler',
  'navGroup.signals':    'Sinyaller',
  'navGroup.workspaces': 'Çalışma alanları',
  'navGroup.alerting':   'Alarm yönetimi',
  'navGroup.system':     'Sistem',
  'navGroup.management': 'Yönetim',

  // Sidebar
  'nav.inbox':       'Sorunlar',
  'nav.shift':       'Vardiya özeti',
  'nav.incidents':   'Olaylar',
  'nav.problems':    'Exceptions/Errors', // v0.10.1107 (operatör: "menüde Exceptions/Errors olarak gözüksün")
  'nav.anomalies':   'Anomaliler',
  'nav.rollouts':    'Deployment/Rollout’lar', // v0.10.984 (Rollouts v2 P2.3)
  'nav.analysis':    'Sistem Analizi',
  'nav.services':    'Servisler',
  'nav.endpoints':   'Endpoint’ler',
  'nav.databases':   'Veritabanları',
  'nav.clusters':    'Cluster\u2019lar',
  'nav.messaging':   'Mesajlaşma',
  'nav.external':    'Dış API’ler',
  'nav.hosts':       'Host’lar',
  'nav.traces':      'İzler',
  'nav.metrics':     'Metrikler',
  'nav.logs':        'Loglar',
  // v0.10.1335 — birleşik arama sayfası (/search); kaynak adları (Logs,
  // Traces…) iki dilde aynı: ürün yüzeyi adları, operatör onaylı metin.
  'nav.search':      'Arama',
  'search.placeholder':   'Servis, endpoint, problem, exception, trace, log ara…',
  'search.minChars':      'En az 2 karakter yazın',
  'search.minCharsBody':  'Tek sorgu, seçili zaman aralığında servis, endpoint, problem, exception, trace ve logları arar.',
  'search.loading':       'Aranıyor…',
  'search.error':         'Arama başarısız',
  'search.noMatch':       '“{q}” için sonuç yok',
  'search.noMatchBody':   'Daha kısa bir sorgu, daha geniş bir zaman aralığı deneyin ya da bir trace id yapıştırın.',
  'search.hidden':        'Mevcut filtrelerle sonuç yok',
  'search.hiddenN':       '{n} gizli',
  'search.clearAll':      'Tümünü temizle',
  'search.results':       '{n} sonuç',
  'search.sort':          'Sıralama',
  'search.sort.relevance': 'İlgi',
  'search.sort.newest':   'En yeni',
  'search.sort.oldest':   'En eski',
  'search.sort.severity': 'Önem',
  'search.group':         'Gruplama',
  'search.group.list':    'Liste',
  'search.group.type':    'Türe göre',
  'search.facet.type':    'Tür',
  'search.facet.service': 'Servis',
  'search.facet.env':     'Ortam',
  'search.facet.sev':     'Önem ≥',
  'search.facet.status':  'Problemler',
  'search.facet.scope':   'Servis, ortam ve önem sayıları: gösterilen sonuçlar içinde',
  'search.status.open':   'Açık',
  'search.status.all':    'Tümü',
  'search.type.service':  'Servis',
  'search.type.endpoint': 'Endpoint',
  'search.type.problem':  'Problem',
  'search.type.exception': 'Exception',
  'search.type.trace':    'Trace',
  'search.type.log':      'Log',
  'search.src.unavailable': '{src} okunamadı ({reason})',
  'search.src.needsService': '{src}: servis ya da trace id gerekli',
  'search.src.skipped':   '{src} atlandı ({reason})',
  'search.open.trace':    "Traces'te aç",
  'search.open.log':      "Logs'ta aç",
  'search.open.problem':  "Problems'ta aç",
  'search.open.exception': "Exceptions'ta aç",
  'search.open.service':  'Serviste aç',
  'search.open.endpoint': "Operasyon trace'lerini aç",
  'search.askCosre':      "CoSRE'ye sor",
  'search.seeAll':        '“{q}” için tüm sonuçlar →',
  'nav.explore':     'Keşfet',
  'nav.runbooks':    'Runbook\'lar',
  'nav.dashboards':  'Panolar',
  'nav.profiling':   'Profilleme',
  'nav.ai':          'AI gözlem',
  'nav.alerts':      'Alarmlar',
  'nav.watchers':    'Watcher\'lar',
  'nav.serviceMap':  'Servis haritası',
  'nav.topology':    'Topoloji',
  'nav.clickhouse':  'ClickHouse',
  'nav.elastic':     'Elasticsearch',
  'nav.slos':        'SLO\'lar',
  'nav.monitors':    'Monitörler',
  'nav.events':      'Olaylar',
  'nav.system':      'Sistem',
  'nav.cardinality': 'Kardinalite',
  'nav.cluster':     'Küme',
  'nav.catalog':     'Servis kataloğu',
  'nav.audit':       'Denetim kaydı',
  'nav.sql':         'SQL editörü',
  'nav.query':       'Sorgu (DQL)',
  'nav.statusPage':  'Genel Durum Sayfası',
  'navGroup.community': 'Topluluk',
  // v0.10.1340 — kabuk atlama bağlantısı (AppShell SkipToContent)
  'shell.skipToContent': 'İçeriğe atla',
  // v0.10.1340 END

  // Login
  'login.signIn':      'Giriş yap',
  'login.signingIn':   'Giriş yapılıyor…',
  'login.email':       'E-posta',
  'login.password':    'Parola',
  'login.usernameOrEmail': 'Kullanıcı adı veya e-posta',
  'login.signInWith':  'Şununla giriş yap:',
  'login.orLocal':     'veya yerel hesapla giriş yap',
  'login.signInToContinue': 'Devam etmek için giriş yapın',
  'login.invalid':     'Geçersiz kullanıcı adı veya parola',
  'login.invalidHint': 'Parolayı bir dokümandan kopyaladıysanız tire (-) yerine başka bir karakter (–, —, gizli boşluk) yapışmış olabilir; tekrar elle yazıp deneyin.',

  // Common buttons
  'btn.save':    'Kaydet',
  'btn.cancel':  'İptal',
  'btn.delete':  'Sil',
  'btn.reset':   'Sıfırla',
  'btn.create':  'Oluştur',
  'btn.edit':    'Düzenle',
  'btn.close':   'Kapat',
  'btn.refresh': 'Yenile',
  'btn.search':  'Ara',

  // Status / states
  'state.loading':     'Yükleniyor…',
  'state.noData':      'Veri yok',
  'state.failed':      'Yüklenemedi',
  'state.tryWidening': 'Zaman aralığını genişletmeyi deneyin.',

  // User menu
  'user.signOut':        'Çıkış yap',
  'user.changePassword': 'Parolayı değiştir',
  'user.manageUsers':    'Kullanıcıları yönet',
  'user.settings':       'Ayarlar',

  // Common labels
  'label.service':    'Servis',
  'label.severity':   'Önem',
  'label.status':     'Durum',
  'label.started':    'Başladı',
  'label.duration':   'Süre',
  'label.errorRate':  'Hata oranı',
  'label.latency':    'Gecikme',
  'label.count':      'Adet',
  'label.value':      'Değer',
  'label.threshold':  'Eşik',

  // Severity badges
  'sev.critical': 'KRİTİK',
  'sev.warning':  'UYARI',
  'sev.info':     'BİLGİ',
  'sev.open':     'AÇIK',
  'sev.resolved': 'ÇÖZÜLDÜ',

  // Time range picker (Grafana-parity panel)
  'trp.quickRanges':   'Hızlı aralıklar',
  'trp.absoluteRange': 'Mutlak zaman aralığı',
  'trp.from':          'Başlangıç',
  'trp.to':            'Bitiş',
  'trp.applyRange':    'Zaman aralığını uygula',
  'trp.recentRanges':  'Son kullanılanlar',
  'trp.browserTime':   'Tarayıcı saati',
  'trp.zoomOut':       'Uzaklaş (2×)',
  'trp.prevMonth':     'Önceki ay',
  'trp.nextMonth':     'Sonraki ay',
  'trp.errFrom':       'Geçersiz "Başlangıç" — YYYY-AA-GG SS:dd:ss kullanın',
  'trp.errTo':         'Geçersiz "Bitiş" — YYYY-AA-GG SS:dd:ss kullanın',
  'trp.errOrder':      '"Bitiş", "Başlangıç"tan sonra olmalı',
  'trp.errMax':        'Aralık çok büyük (en fazla 1 yıl)',

  // Quick-range labels
  'range.5m':  'Son 5 dakika',
  'range.15m': 'Son 15 dakika',
  'range.30m': 'Son 30 dakika',
  'range.1h':  'Son 1 saat',
  'range.3h':  'Son 3 saat',
  'range.6h':  'Son 6 saat',
  'range.12h': 'Son 12 saat',
  'range.24h': 'Son 24 saat',
  'range.2d':  'Son 2 gün',
  'range.7d':  'Son 7 gün',
  'range.30d': 'Son 30 gün',

  'ai.askCosre':          'CoSRE’ye sor',
  'ai.askCosreTraceHint': 'Bu trace hakkında CoSRE’ye soru sor.',
  'ai.askCosreSpanHint':  'CoSRE’ye sor · span — bu span’i açıkla.',
  // v0.10.1338
  'ai.askCosreLogHint':      'CoSRE’ye sor · log — bu log satırını açıkla.',
  'ai.askCosreLogSpikeHint': 'CoSRE’ye sor · log artışı — bu log artışını açıkla.',
  'ai.askCosreEndpointHint': 'CoSRE’ye sor · endpoint — ne oluyor, ne zamandan beri, süre nereye gidiyor, hangi hatalar.', // v0.10.1367
  'ai.subject.trace':     'trace',

  'ai.ctx.label':     'Bağlam',
  'ai.ctx.aria':      'CoSRE sohbet bağlamı',
  'ai.ctx.trace':     'Trace',
  'ai.ctx.span':      'Span',
  'ai.ctx.service':   'Servis',
  'ai.ctx.env':       'Ortam',
  'ai.ctx.clusterNs': 'Cluster / namespace',
  'ai.ctx.window':    'Pencere',
  'ai.ctx.saved':     'Konuşmayla kaydedilen bağlam — canlı sayfa farklı olabilir.',

  'cosre.cap.aria':             'CoSRE neler yapabilir',
  'cosre.cap.tryHint':          'Kutuya örnek bir soru yazar — düzenleyip gönder',
  'cosre.cap.wiki':             'Kurum wiki’sinde arayabilirim — ör. “wikide cache refresh nasıl yapılır”',
  'cosre.cap.wiki.prompt':      'wikide cache refresh nasıl yapılır',
  'cosre.cap.service':          'Bir servisin trace’lerine, hatalarına, gecikmesine bakabilirim — servis adını yaz (@ ile tamamla)',
  'cosre.cap.service.prompt':   '@| servisinin son 1 saatteki trace\'leri, hataları ve gecikmesi?',
  'cosre.cap.operation':        'Hata veren teknik operasyonları inceleyebilirim — operasyon adını yaz',
  'cosre.cap.operation.prompt': '| operasyonu neden hata veriyor?',
  'cosre.cap.navigate':         'Nereye bakman gerektiğini söyleyip ilgili sayfaya yönlendirebilirim',
  'cosre.cap.navigate.prompt':  '| sorununu incelemek için nereye bakmalıyım?',
  'cosre.chip.wiki':            'Wikide ara: …',
  'cosre.chip.wiki.prompt':     'wikide ',

  'rootCause.bubbleUnavailable': 'Attribute kıyası okunamadı — span taraması düştü ya da zaman aşımına uğradı. Bu, "ayrışan boyut yok" demek değildir.',

  'sso.trustEmail.label':        'Doğrulanmamış e-postaya güven (önerilmez)',
  'sso.trustEmail.warning':      'email_verified=false olan girişler yalnız izinli alan adlarından kabul edilir. Risk: e-posta çarpışmasıyla hesap ele geçirme — yalnız kullanıcının e-postasını kendisinin belirleyemediği/değiştiremediği kurumsal IdP\'de (self-registration, e-posta düzenleme, sosyal/brokered IdP yok) ve izinli alan adları tanımlıyken kullanın. Admin ve yerel/LDAP hesaplar bu yolla bağlanmaz. Tercih edilen düzeltme IdP tarafında (Keycloak "Trust Email").',
  'sso.trustEmail.needsDomains': 'Önce izinli alan adlarını girin — liste boşken doğrulanmamış e-postaya güvenilemez.',

  'sso.usernameFallback.label':   'E-posta yoksa kullanıcı adıyla eşleştir (AD/LDAP)',
  'sso.usernameFallback.on':      'Açık: id_token ve UserInfo e-posta taşımıyorsa doğrulanmış id_token\'daki kullanıcı adı claim\'i AD/LDAP\'ta (tam eşleşme) aranır ve dizindeki e-posta kullanılır; LDAP yoksa (ya da dizinde e-posta yoksa) kullanıcı LDAP kullanıcı adıyla mevcut Coremetry kullanıcısına eşlenir. E-postasız yeni kullanıcı açılmaz. İzinli alan adları yine uygulanır.',
  'sso.usernameFallback.off':     'Kapalı (varsayılan): id_token ve UserInfo e-posta taşımıyorsa giriş reddedilir (email_missing). UserInfo her zaman denenir. Tercih edilen düzeltme IdP\'de: Keycloak LDAP federasyonunda mail → email eşleyicisi ve email client scope\'u Default + "Add to ID token".',
  'sso.usernameFallback.risk':    'Yalnız kullanıcı bu claim\'i IdP\'de değiştiremiyorsa kullanın (LDAP federasyonu salt-okunur, kullanıcı adı düzenleme kapalı). Brokered (dış/sosyal) IdP, self-registration ya da düzenlenebilir kullanıcı adı varsa GÜVENSİZ — kullanıcı adını seçebilen o hesabı açar. Admin hesapları aşağıda ayrıca izin verilmedikçe bu yolla açılmaz.',
  'sso.usernameAdmin.label':      'Kullanıcı adı eşleştirmesiyle admin hesaplarını da aç (önerilmez)',
  'sso.usernameAdmin.warning':    'Admin hesabına IdP\'de bu kullanıcı adını denetleyen kişi erişir. Kapalı bırakın; adminler parola/LDAP ile girebilir.',
  'sso.usernameClaim.label':      'Kullanıcı adı claim\'i',
  'sso.usernameClaim.hint':       'Yalnız bu claim okunur (varsayılan preferred_username = AD sAMAccountName). Harf, rakam ve _ - . : ; profil/e-posta claim\'leri seçilemez.',

  // v0.10.1334 — uygulama içi rehberli tur (features/tour), R1.
  'tour.chrome.dialog':     'Rehberli tur',
  'tour.chrome.stepOf':     'Adım {n} / {total}',
  'tour.chrome.progress':   'Tur ilerlemesi',
  'tour.chrome.back':       'Geri',
  'tour.chrome.next':       'İleri',
  'tour.chrome.finish':     'Bitir',
  'tour.chrome.skip':       'Turu atla',
  'tour.chrome.restart':    'Turu yeniden başlat',
  'tour.chrome.loading':    'Sayfanın bu bölümü aranıyor…',
  'tour.note.timeout':      'Veri henüz yok — sayfanın bu bölümü görünmedi. Yine de devam edebilirsin.',
  'tour.note.not_yet':      'Sonraki sürümde geliyor.',
  'tour.note.no_services':  'Henüz servis raporlamıyor — OTLP verisi gelince tur anlamlı olur.',
  'tour.welcome.title':     'Hoş geldin — iki dakikalık tur',
  'tour.welcome.body':      'Bu kurulumun şu an, son 5 dakikada gördükleri. Sırada Servisler listesi var — incelemelerin çoğu oradan başlar.',
  'tour.welcome.unavailable': 'Canlı sayılar okunamadı — tur yine de çalışır.',
  'tour.welcome.tile.services': 'Servis',
  'tour.welcome.tile.spans':    'Span · 5 dk',
  'tour.welcome.tile.spansTop': 'Span · 5 dk · ilk 50',
  'tour.welcome.tile.busiest':  'En yoğun servis',
  'tour.welcome.tile.envs':     'Ortam',
  'tour.services.title':    'Servisler: başlangıç noktan',
  'tour.services.body':     'Her satır OTLP span gönderen bir servis. Adın önündeki nokta sağlığıdır: hata oranı %1\'i aşınca (ya da açık bir uyarı/bilgi problemi varsa) amber, %5\'i aşınca (ya da açık kritik problem varsa) kırmızı. Spans, Error rate ve P99 seçili zaman aralığını kapsar; önceki pencereyle kıyas için "Δ prior"u aç. Servisi açmak için satıra tıkla.',
  // v0.10.1336 — rehberli tur R2: adım 3–5 (operations / traces / cosre) + alt adımlar.
  'tour.operations.title':  'Operasyonlar: zaman nereye gidiyor',
  'tour.operations.header': 'Bu, en yoğun servisin sayfası. Adın üstündeki kırıntı aynı zaman penceresiyle Servisler\'e döner, ☆ servisi Cmd-K hızlı erişimine sabitler; bu düğmeler servisin backtrace, log, endpoint, problem, anomali ve trace\'lerine atlar — servis ve pencere de yanında gelir.',
  'tour.operations.tab':    'Operations sekmesi bu servisin her operasyonunu (span adı) listeler. Sekme URL\'de durur (?tab=operations); paylaşılan bağlantı doğrudan burada açılır.',
  'tour.operations.impact': 'Satırlar Impact = ortalama süre × çağrı sayısı ile sıralanır: kullanıcılarına toplamda en çok zaman kaybettiren operasyon en üstte — yalnız en yavaşı değil. Raw span adlarını geldiği gibi gösterir; Normalized (sağ üstte) kimlik taşıyan adları GET /users/:id gibi şekillerde toplar.',
  'tour.operations.body':   'Her satır bir operasyon: trendi, çağrı sayısı, hata oranı ve gecikme yüzdelikleri. Satıra tıklayınca o operasyonun trace\'leri açılır — sıradaki adım tam oraya gidiyor.',
  'tour.traces.title':      'Trace\'ler: yavaş ve hatalı istekler',
  'tour.traces.filters':    'Bu servisin trace\'lerini status_code = error hızlı çipi uygulanmış olarak açtık. Etkin süzgeçler bu çubukta (tıkla: düzenle, ✕: kaldır); alttaki "Hızlı:" satırı sık kullanılanları — hatalar, server span\'ler, POST — tek tıkla ekler.',
  'tour.traces.minms':      'Min ms = 500 hızlı istekleri gizler; geriye yalnız yavaş ve hatalı trace\'ler kalır. Soldaki servis seçici listeyi daraltır; bir alanı değiştirince Search\'e bas (ya da Enter).',
  'tour.traces.body':       'Her satır bir trace — servisler boyunca tek bir istek. Satıra tıklayınca şelalesi açılır: her span, süresi ve öznitelikleri.',
  'tour.cosre.title':       'CoSRE: düz dille sor',
  'tour.cosre.fab':         'Bu düğme CoSRE\'yi açar — telemetrini okuyan asistan. Bulunduğun sayfayı bilir: bir servis ya da trace üstündeyken o bağlamdan başlar. Kırmızı rozet açık kritik problemleri sayar.',
  'tour.cosre.body':        'Bu servis için bir soru taslağı hazırladık — hiçbir şey gönderilmedi. Bitir\'e bas, istersen düzenle, sonra Enter ile gönder. Esc önce sohbet çekmecesini kapatır; ikinci Esc turu atlar.',
  'tour.cosre.prompt':      'Son 1 saatte {svc} hatalı mı?',
  'tour.cosre.promptFleet': 'Şu an önce neye bakmalıyım?',
  'tour.note.copilot_disabled': 'CoSRE bu kurulumda kapalı — admin Settings › CoSRE\'den açar.',
  // v0.10.1336 END
};

const CATALOGS: Record<Lang, Catalog> = { en: EN, tr: TR };

// User-level language override. Stored in localStorage so it
// survives reloads without a server round trip and so unauthed
// pages (the public-trace viewer, the login screen) can also
// honour the picked language. When unset, the branding-level
// language wins — operators who never touch the picker get the
// org default they always had.
const USER_LANG_KEY = 'coremetry.lang';
const USER_LANG_EVENT = 'coremetry:lang-change';

function readUserLang(): Lang | null {
  if (typeof window === 'undefined') return null;
  const v = getRaw(USER_LANG_KEY);
  if (v === 'tr' || v === 'en') return v;
  return null;
}

// useUserLang subscribes to localStorage + the same-tab
// USER_LANG_EVENT. The storage listener fires on OTHER tabs;
// the custom event fires within the current tab where the
// picker was clicked, so both paths flow back to every
// useT consumer immediately.
export function useUserLang(): Lang | null {
  const [lang, setLang] = useState<Lang | null>(() => readUserLang());
  useEffect(() => {
    const handler = () => setLang(readUserLang());
    window.addEventListener(USER_LANG_EVENT, handler);
    window.addEventListener('storage', handler);
    return () => {
      window.removeEventListener(USER_LANG_EVENT, handler);
      window.removeEventListener('storage', handler);
    };
  }, []);
  return lang;
}

// setUserLang persists the choice + broadcasts the same-tab
// event so every useT consumer re-renders without a manual
// reload. Passing null clears the override (falls back to
// branding).
export function setUserLang(lang: Lang | null): void {
  if (typeof window === 'undefined') return;
  if (lang) setRaw(USER_LANG_KEY, lang);
  else removeRaw(USER_LANG_KEY);
  window.dispatchEvent(new Event(USER_LANG_EVENT));
}

// lastResolvedLang — v0.10.948: useLang'in en son çözdüğü etkin dil (marka
// varsayılanı dahil); hiçbir bileşen henüz çizilmediyse null.
let lastResolvedLang: Lang | null = null;

// useLang resolves the EFFECTIVE language: user-picked
// (localStorage) → branding default → English. Exported for
// components that need locale-aware formatting beyond catalog
// lookups (e.g. TimeRangePicker's month/weekday tables).
export function useLang(): Lang {
  const brand = useBranding();
  const userLang = useUserLang();
  const lang = userLang ?? (brand.language === 'tr' ? 'tr' : 'en');
  // v0.10.948 — hook DIŞI metin üreticileri (aiSubjectTitle: çekmece başlığı
  // ve Geçmiş satırı) için son çözülen dil. İdempotent önbellek: aynı girdi
  // aynı değeri yazar, render saflığını bozmaz.
  lastResolvedLang = lang;
  return lang;
}

// currentLang — v0.10.948: hook kullanamayan saf yardımcıların etkin dili.
// Öncelik useT ile aynı: kullanıcı seçimi (localStorage) → son çözülen
// (marka varsayılanı) → İngilizce.
export function currentLang(): Lang {
  return readUserLang() ?? lastResolvedLang ?? 'en';
}

// useT returns a translator scoped to the effective language.
// Priority: user-picked (localStorage) → branding default →
// English fallback. Hook so any of those layers changing
// (admin saves new branding → invalidateBranding fires; user
// clicks the picker → USER_LANG_EVENT fires) flows through
// every consumer without a manual reload.
export function useT(): (key: string) => string {
  const cat = CATALOGS[useLang()];
  return (key: string) => cat[key] ?? EN[key] ?? key;
}

// Direct catalog access for non-component code paths (e.g. an
// imperative toast). Mirrors useT but reads the cached branding
// synchronously.
export function t(key: string, lang: Lang = 'en'): string {
  return CATALOGS[lang][key] ?? EN[key] ?? key;
}

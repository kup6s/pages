# kup6s-pages: Konzept

Ein Kubernetes-nativer Service für statisches Website-Hosting, inspiriert von GitHub Pages.

## Problemstellung

Bestehende Lösungen für statisches Website-Hosting auf Kubernetes sind entweder ineffizient oder schwer zu integrieren:

**Kubero** und ähnliche PaaS-Lösungen starten einen Pod pro Website. Bei vielen kleinen statischen Sites führt das zu erheblichem Ressourcen-Overhead.

**Codeberg pages-server** ist effizient (ein Container für alle Sites), macht aber eigenes TLS-Handling. Das kollidiert mit dem Standard-Kubernetes-Pattern (Ingress Controller + cert-manager) und erfordert SSL-Passthrough, was Layer-7-Features wie Path-Routing verhindert.

**git-sync als Sidecar** synchronisiert nur ein Repository pro Container. Für viele Sites bräuchte man viele Sidecars.

## Designziele

1. **Ressourceneffizienz**: Ein nginx-Pod served alle Sites
2. **Kubernetes-nativ**: Integration mit Traefik IngressController und cert-manager
3. **Deklarativ**: Sites werden als Custom Resources definiert
4. **Git-basiert**: Automatische Synchronisation aus Git-Repositories
5. **Custom Domains**: Jede Site kann eine eigene Domain haben
6. **Einfach**: Minimale Konfiguration für den Endnutzer

## Architektur

### Übersicht

```
┌─────────────────────────────────────────────────────────────────────────┐
│                                                                         │
│  ┌──────────────┐      ┌──────────────┐      ┌──────────────┐          │
│  │ StaticSite   │      │ StaticSite   │      │ StaticSite   │   ...    │
│  │ CRD          │      │ CRD          │      │ CRD          │          │
│  └──────┬───────┘      └──────┬───────┘      └──────┬───────┘          │
│         │                     │                     │                   │
│         └─────────────────────┼─────────────────────┘                   │
│                               │                                         │
│                               ▼                                         │
│                      ┌────────────────┐                                 │
│                      │    Operator    │                                 │
│                      │                │                                 │
│                      │ • Watched CRDs │                                 │
│                      │ • Erstellt:    │                                 │
│                      │   - Ingress    │                                 │
│                      │   - Middleware │                                 │
│                      │   - Certific.  │                                 │
│                      └────────────────┘                                 │
│                                                                         │
│         ┌──────────────────────┼──────────────────────┐                 │
│         │                      │                      │                 │
│         ▼                      ▼                      ▼                 │
│  ┌────────────┐        ┌─────────────┐        ┌─────────────┐          │
│  │ Traefik    │        │ Traefik     │        │ cert-manager│          │
│  │ Ingress-   │        │ Middleware  │        │ Certificate │          │
│  │ Route      │        │ (addPrefix) │        │             │          │
│  └────────────┘        └─────────────┘        └─────────────┘          │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│                                                                         │
│                        ┌────────────────┐                               │
│                        │     Syncer     │                               │
│                        │                │                               │
│                        │ • Liest CRDs   │                               │
│                        │ • Git clone/   │                               │
│                        │   pull         │                               │
│                        │ • Webhook API  │                               │
│                        └───────┬────────┘                               │
│                                │                                        │
│                                ▼                                        │
│                     ┌─────────────────────┐                             │
│                     │   PVC: /sites       │                             │
│                     │   ├── site-a/       │                             │
│                     │   ├── site-b/       │                             │
│                     │   └── site-c/       │                             │
│                     └──────────┬──────────┘                             │
│                                │                                        │
│                                ▼                                        │
│                     ┌─────────────────────┐                             │
│                     │   nginx (1 Pod)     │                             │
│                     │   root /sites;      │                             │
│                     └─────────────────────┘                             │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### Komponenten

#### 1. StaticSite CRD

Die Custom Resource Definition ist das zentrale Konfigurationselement:

```yaml
apiVersion: pages.kup6s.io/v1alpha1
kind: StaticSite
metadata:
  name: kunde-website      # Wird zum Pfad /kunde-website
  namespace: pages
spec:
  repo: https://forgejo.kup6s.io/kunde/website.git
  branch: main             # Optional, default: main
  path: /dist              # Optional, default: / (Repo-Root)
  domain: www.kunde.at     # Optional, sonst: <name>.pages.kup6s.io
  secretRef:               # Optional, für private Repos
    name: git-credentials
    key: password
  syncInterval: 5m         # Optional, default: 5m
```

Der `metadata.name` ist zentral: Er definiert den Pfad unter dem die Site im nginx liegt (`/sites/<name>/`).

#### 2. Operator

Der Operator watched StaticSite-Ressourcen und erstellt für jede:

**Traefik Middleware (addPrefix)**
```yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: kunde-website-prefix
spec:
  addPrefix:
    prefix: /kunde-website
```

**Traefik IngressRoute**
```yaml
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata:
  name: kunde-website
spec:
  entryPoints: [websecure]
  routes:
    - match: Host(`www.kunde.at`)
      middlewares:
        - name: kunde-website-prefix
      services:
        - name: static-sites-nginx
          namespace: kup6s-pages
          port: 80
  tls:
    secretName: kunde-website-tls
```

**cert-manager Certificate** (nur bei custom domain)
```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: kunde-website-tls
spec:
  secretName: kunde-website-tls
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
  dnsNames:
    - www.kunde.at
```

Der Operator setzt Owner References, sodass alle erstellten Ressourcen automatisch gelöscht werden, wenn die StaticSite gelöscht wird.

#### 3. Syncer

Ein zentraler Service der alle StaticSites synchronisiert:

- Läuft als Deployment mit Zugriff auf das PVC
- Pollt periodisch alle StaticSite CRDs (default: alle 5 Minuten)
- Klont neue Repos, pullt bestehende
- Unterstützt private Repos via Secrets
- Bietet HTTP-API für Webhooks (Instant-Sync bei Push)

**Webhook-Endpoints:**
- `POST /sync/{namespace}/{name}` - Sync einer spezifischen Site
- `POST /webhook/forgejo` - Forgejo/Gitea Push-Webhook
- `POST /webhook/github` - GitHub Push-Webhook
- `GET /health` - Health Check

#### 4. nginx

Ein einzelnes nginx-Deployment served alle Sites:

```nginx
server {
    listen 80;
    root /sites;
    
    location / {
        try_files $uri $uri/ $uri/index.html =404;
    }
}
```

Die Konfiguration ist statisch und muss nie angepasst werden. Das Routing übernimmt Traefik via addPrefix.

#### 5. Shared PVC

Ein PersistentVolumeClaim mit ReadWriteMany (RWX):
- Syncer schreibt: `/sites/<name>/`
- nginx liest: `/sites/<name>/`

## Request-Flow

```
HTTPS Request: www.kunde.at/about.html
         │
         │ 1. TLS Termination (Traefik)
         ▼
┌─────────────────────────────────────────────────────────┐
│  Traefik                                                │
│                                                         │
│  Route Match: Host(`www.kunde.at`)                      │
│  Middleware:  addPrefix(/kunde-website)                 │
│                                                         │
│  Interner Request: /kunde-website/about.html            │
└────────────────────────┬────────────────────────────────┘
                         │
                         │ 2. HTTP zu nginx Service
                         ▼
┌─────────────────────────────────────────────────────────┐
│  nginx                                                  │
│                                                         │
│  root /sites;                                           │
│  Request: /kunde-website/about.html                     │
│  Served:  /sites/kunde-website/about.html               │
└────────────────────────┬────────────────────────────────┘
                         │
                         │ 3. Datei aus PVC
                         ▼
┌─────────────────────────────────────────────────────────┐
│  PVC: /sites                                            │
│                                                         │
│  /sites/kunde-website/                                  │
│  ├── index.html                                         │
│  ├── about.html  ◄── Diese Datei                        │
│  └── assets/                                            │
└─────────────────────────────────────────────────────────┘
```

## Sync-Flow

### Periodischer Sync

```
┌──────────────┐     ┌─────────────────────────────────────┐
│   Syncer     │     │  Kubernetes API                     │
│              │     │                                     │
│  Timer: 5m   │────▶│  GET /apis/pages.kup6s.io/v1alpha1/ │
│              │     │      staticsites                    │
└──────┬───────┘     └─────────────────────────────────────┘
       │
       │ Für jede StaticSite:
       ▼
┌──────────────────────────────────────────────────────────┐
│                                                          │
│  if /sites/<name>/.git existiert:                        │
│      git pull                                            │
│  else:                                                   │
│      git clone --depth=1 <repo> /sites/<name>            │
│                                                          │
│  Status Update: lastSync, lastCommit                     │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

### Webhook-Sync (Instant)

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│   Forgejo    │     │   Traefik    │     │   Syncer     │
│              │     │              │     │              │
│  git push    │────▶│  webhook.    │────▶│  POST        │
│              │     │  pages.      │     │  /webhook/   │
│              │     │  kup6s.io    │     │  forgejo     │
└──────────────┘     └──────────────┘     └──────┬───────┘
                                                 │
       ┌─────────────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────────────────────────┐
│                                                          │
│  1. Parse Webhook Payload (repo URL, branch)             │
│  2. Finde alle StaticSites mit dieser Repo URL           │
│  3. git pull für jede matching Site                      │
│  4. Status Update                                        │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

## Vorteile dieses Designs

### Ressourceneffizienz

| Ansatz | 100 Sites | 1000 Sites |
|--------|-----------|------------|
| Pod pro Site | 100 Pods | 1000 Pods |
| kup6s-pages | 3 Pods | 3 Pods |

Die drei Pods sind: Operator (1), Syncer (1), nginx (1-2 für HA).

### Keine dynamische nginx-Konfiguration

Das addPrefix-Pattern eliminiert die Notwendigkeit, nginx bei jeder neuen Site neu zu konfigurieren:

- Keine ConfigMap-Updates
- Kein nginx-Reload
- Keine Race Conditions

### Kubernetes-native Integration

- **Traefik**: Standard-IngressController, volle Feature-Unterstützung
- **cert-manager**: Automatische TLS-Zertifikate, auch für Custom Domains
- **RBAC**: Feingranulare Berechtigungen
- **Owner References**: Automatisches Cleanup

### Einfache Erweiterbarkeit

Zukünftige Features können einfach hinzugefügt werden:

- **Basic Auth**: Zusätzliche Traefik-Middleware
- **Rate Limiting**: Traefik-Middleware
- **Custom Headers**: Traefik-Middleware
- **Redirects**: Traefik-Middleware

## Deployment-Übersicht

```
Namespace: kup6s-pages (System)
├── Deployment: pages-operator
│   └── Pod: operator
├── Deployment: pages-syncer
│   └── Pod: syncer
├── Deployment: static-sites-nginx
│   └── Pod: nginx (replicas: 2)
├── Service: static-sites-nginx
├── Service: pages-syncer (für Webhooks)
├── PVC: static-sites-data
├── ConfigMap: nginx-config
└── ServiceAccounts + RBAC

Namespace: pages (User Sites)
├── StaticSite: kunde-a-website
├── StaticSite: kunde-b-docs
├── Secret: git-credentials (optional)
├── IngressRoute: kunde-a-website (generated)
├── IngressRoute: kunde-b-docs (generated)
├── Middleware: kunde-a-website-prefix (generated)
├── Middleware: kunde-b-docs-prefix (generated)
├── Certificate: kunde-a-website-tls (generated)
└── Certificate: kunde-b-docs-tls (generated)
```

## Limitierungen

1. **RWX Storage erforderlich**: Das PVC muss ReadWriteMany unterstützen (z.B. Longhorn, NFS, CephFS)

2. **Keine Build-Pipeline**: kup6s-pages served nur statische Dateien. Build-Schritte (z.B. npm build) müssen vorher in CI/CD erfolgen

3. **Keine Preview-Deployments**: Jede StaticSite ist eine feste Konfiguration, keine automatischen Branch-Previews

4. **Single Point of Sync**: Der Syncer ist ein einzelner Pod. Bei Ausfall verzögern sich Updates (aber Serving funktioniert weiter)

## Zukünftige Erweiterungen

- **Preview Deployments**: Automatische Sites für Pull Requests
- **Build Integration**: Optional Build-Container vor Sync
- **Metrics**: Prometheus-Metriken für Sync-Status und Fehler
- **UI**: Web-Dashboard für Site-Verwaltung
- **Multi-Cluster**: Sync zu mehreren Clustern

## Fazit

kup6s-pages bietet eine ressourceneffiziente, kubernetes-native Lösung für statisches Website-Hosting. Durch die Kombination von CRD-basierter Konfiguration, zentralem Git-Sync und dem addPrefix-Pattern wird die Komplexität minimiert, während volle Integration mit dem Kubernetes-Ökosystem gewährleistet ist.

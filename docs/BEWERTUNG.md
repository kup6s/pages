# Kritische Bewertung: kup6s-pages Konzept

## Konzept-Stärken

### 1. Architektur ist clever
Das addPrefix-Pattern mit Traefik Middleware ist eine elegante Lösung - keine dynamische nginx-Konfiguration nötig.

### 2. Ressourceneffizienz
Ein nginx-Pod für alle Sites ist ein großer Vorteil gegenüber Pod-per-Site Lösungen.

| Ansatz | 100 Sites | 1000 Sites |
|--------|-----------|------------|
| Pod pro Site | 100 Pods | 1000 Pods |
| kup6s-pages | 3 Pods | 3 Pods |

### 3. Kubernetes-native Integration
- CRDs mit Status-Subresource
- cert-manager für automatische TLS-Zertifikate
- Traefik IngressRoutes für Routing
- Owner References für automatisches Cleanup

### 4. Separation of Concerns
Operator und Syncer sind sauber getrennt - Operator managed Kubernetes-Ressourcen, Syncer managed Git-Sync.

---

## Kritische Probleme

### 1. `spec.path` ist nicht implementiert

In `pkg/syncer/git.go:169-175` wird `setupSubpath` aufgerufen, aber die Funktion tut nichts:

```go
func (s *Syncer) setupSubpath(repoDir, subpath string) error {
    return nil  // <- Tut nichts!
}
```

**Problem**: Wenn jemand ein Repo mit Build-Output in `/dist` hat, funktioniert das Serving nicht. Der addPrefix fügt nur den Site-Namen hinzu, nicht den Subpfad.

**Lösung nötig**: Entweder Symlinks erstellen oder nginx-Location-Rewrite.

### 2. Import-Pfad Mismatch

- `go.mod`: `module github.com/kup6s/pages`
- Code imports: `github.com/kleinundpartner/kup6s-pages/...`

Das wird beim Build sofort scheitern.

### 3. DeepCopy-Generierung fehlt

Die kubebuilder-Marker sind da (`+k8s:deepcopy-gen`), aber es gibt keine `zz_generated.deepcopy.go`. Ohne diese wird die Scheme-Registrierung fehlschlagen - controller-runtime erwartet DeepCopy-Methoden.

### 4. Patch-Type ist falsch

In `pkg/syncer/git.go:226`:
```go
Patch(ctx, site.Name, "application/merge-patch+json", ...)
```

Sollte `types.MergePatchType` sein (ein `types.PatchType`, kein String).

### 5. Keine Cleanup-Logik für gelöschte Sites

In `pkg/controller/staticsite.go:311-312` steht ein TODO-Kommentar:
```go
// Hier könnten wir den Syncer triggern um /sites/<n>/ zu löschen
```

Die Dateien auf dem PVC werden nie gelöscht - Speicher-Leak über Zeit.

### 6. `syncInterval` pro Site wird ignoriert

`spec.syncInterval` ist im CRD definiert, aber der Syncer nutzt nur den globalen `DefaultInterval`. Alle Sites werden mit demselben Interval gesynct.

### 7. Wildcard-Certificate Annahme

Bei Sites ohne custom domain wird `pkg/controller/staticsite.go:191` `pages-wildcard-tls` referenziert, aber nirgends erstellt. Das Konzept erwähnt es nicht explizit als Voraussetzung.

---

## Sicherheitslücken

### SSRF-Risiko
Der Syncer akzeptiert beliebige Repo-URLs ohne Validierung:
- `file://` URLs möglich
- Interne Netzwerk-IPs erreichbar
- Keine Allowlist für Git-Hosts

### Secret-Zugriff zu weitreichend
Syncer braucht cluster-weite Secret-Leserechte, da er Secrets aus User-Namespaces lesen muss. Das ist sicherheitskritisch.

### Webhook-Secret nicht validiert
`WebhookSecret` ist im Code definiert aber nie validiert - jeder kann Webhooks triggern und damit DoS verursachen.

---

## Kleinere Punkte

- Keine Health-Probes im Operator-Deployment (nur im Syncer)
- `error_page 404 /404.html` in nginx referenziert eine Datei die im `/sites`-Root liegen müsste, nicht pro Site
- `runAsUser: 1000` im Syncer - git-Operationen brauchen Schreibrechte auf PVC
- Shallow Clone + Pull: `Depth: 1` beim Pull kann bei größeren Branch-Divergenzen zu Problemen führen

---

## Fazit

Das Konzept ist **architektonisch solide** und das addPrefix-Pattern ist clever.

---

## Erledigte Fixes

Die folgenden Probleme wurden behoben:

- [x] Import-Pfade korrigiert (`github.com/kup6s/pages`)
- [x] DeepCopy-Methoden generiert (`zz_generated.deepcopy.go`)
- [x] Patch-Type korrigiert (`types.MergePatchType`)
- [x] `spec.path` implementiert (Symlink-Ansatz: Repos in `.repos/<name>`, Symlink nach `<name>`)
- [x] Cleanup-Logik für gelöschte Sites (periodisch + DELETE-Endpoint)
- [x] Webhook-Secret-Validierung (HMAC-SHA256 für GitHub/Forgejo)
- [x] SSRF-Protection (Git-Host-Allowlist mit `--allowed-hosts` Flag)
- [x] controller-runtime v0.18 API-Kompatibilität

### Verbleibende Punkte

- `syncInterval` pro Site wird noch nicht individuell berücksichtigt
- Health-Probes im Operator-Deployment
- Secret-Scoping (Syncer braucht aktuell cluster-weite Secret-Leserechte)

# `php127` — Machine Virtuelle PHP 8.3 en Go 1.27 Pur (Zéro CGO), Noyaux SIMD AVX2 & Observabilité Matérielle PMU

[![Language: Go 1.27](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go)](https://go.dev/)
[![Zero CGO](https://img.shields.io/badge/CGO-0%25%20Pure%20Go-success)](#)
[![SIMD: sgoiter AVX2](https://img.shields.io/badge/SIMD-sgoiter%20AVX2-blue)](#)
[![English README](https://img.shields.io/badge/Language-English-blue)](README.md)

`php127` est un moteur d'exécution PHP 8.3 autonome écrit en Go 1.27 pur (zéro CGO, zéro dépendance externe). Il remplace les pointeurs d'union C (`zval`) et le comptage de références par valeur (`Rc<RefCell<...>>`) par une **structure `types.Value` de 16 octets sans pointeur** (deux registres de 64 bits) adossée à une **arène `Zone` isolée par requête** dont la réinitialisation (`Zone.Reset()`) s'exécute en **195 instructions CPU**.

---

## Monographie Technique & Bancs de Mesure (`docs/`)

La monographie complète (incluant les 15 planches haute définition issues des compteurs matériels Linux `perf_event_open` et le guide opérationnel de sécurité et d'observabilité) est versionnée dans [`docs/article_x_micro_design_for_cybersafety.docx`](docs/article_x_micro_design_for_cybersafety.docx), accompagnée de l'instantané JSON [`probebench/testdata/latest_metrics.json`](probebench/testdata/latest_metrics.json).

---

## Couverture WordPress 6.9.4 & Extensions Zéro-CGO

`php127` exécute l'ensemble de la surface PHP 8.3 requise par WordPress 6.9.4 en Go pur (zéro CGO) :
- **Instantanés de bootstrap `ARCHTIME` en `ProcessZone` (`vm/archtime.go`, `types/string.go`)** : promotion en mémoire de processus immuable (`RefCount = math.MaxUint32`, séparation Copy-on-Write à l'écriture) des fonctions, classes, constantes, grands tableaux (`$allowedposttags`) et objets globaux (`WP_Token_Map`).
- **Base de données (`mysqli/`, `pdo/`)** : API `mysqli` procédurale et objet complète (protocole MySQL natif ou traducteur SQLite embarqué sans CGO) et `PDO` (`sqlite`).
- **Médias, Archives & Fileinfo (`ext/gd.go`, `stdlib/wp_sys.go`)** : `GdImage` (`WP_Image_Editor_GD`, redimensionnement bilinéaire `imagecopyresampled`, rotation, retournement, PNG/JPEG/GIF avec contrôle des dimensions avant décodage), `finfo` (`finfo_open`, `finfo_file`, `finfo_buffer`, `mime_content_type`) et `PclZip`.
- **Transport HTTP, XML & Protocoles de flux (`ext/curl_xml.go`, `streams/userwrapper.go`, `vm/userwrapper.go`)** : `CurlHandle` (`curl_exec` avec rappels `CURLOPT_WRITEFUNCTION` et `CURLOPT_HEADERFUNCTION` pour `WpOrg\Requests\Transport\Curl`), parseur SAX `XMLParser` (`xml_parse`, `xml_parse_into_struct` pour `IXR_Message`), sockets TLS/asynchrones et protocole complet `stream_wrapper_register`.
- **Cryptographie, I18n & SAPI Multipart (`ext/openssl_pkey.go`, `ext/intl.go`, `stdlib/bcrypt.go`, `sapi/superglobals.go`)** : signatures OpenSSL (RSA/ECDSA/Ed25519), UTS#46 `idn_to_ascii`/`idn_to_utf8`, `Normalizer` Unicode, Eksblowfish `$2y$`/`$2a$`/`$2b$`, `php://input` et `multipart/form-data` `$_FILES` (clés à crochets et codes `UPLOAD_ERR_*`).

---

## Démarrage Rapide

```bash
# Compiler le binaire autonome
go build -o php127 ./cmd/php127

# Exécuter les tests avec la garde d'invariants de pile (-tags vmcheck)
go test -tags vmcheck -race -count=1 ./zone/... ./types/... ./lexer/... ./parser/... ./bytecode/... ./compiler/... ./stdlib/... ./ext/... ./mysqli/... ./streams/... ./vm/... ./sapi/...

# Lancer les sondes matérielles PMU (M0a..M6 et matrice 2D F x D)
go test -v -count=1 -run "TestAtomicMicroProbes|TestStratifiedFunctionAndDataMatrix" ./probebench/...
```


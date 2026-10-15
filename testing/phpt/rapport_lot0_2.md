# Rapport du lot 0.2

## Ordre de réalisation du lot 0.2, tentative 1

Le paquet /devhoros/php127/testing/phpt et le lanceur /devhoros/php127/cmd/phpt-runner implémentent les modes autonome et différentiel. Les cinq fichiers demandés sont présents ; /devhoros/php127/cmd/phpt-runner/main_test.go et une épreuve officielle complètent leurs vérifications. L'inventaire fonctionnel comprend sept fichiers, tous lus et vérifiés dans /devhoros/php127/testing/phpt/lot0.2.work.

Le parseur conserve les octets des sections, y compris leurs fins de ligne. Il refuse les sections dupliquées, les variantes concurrentes de FILE et EXPECT, les sections requises absentes et les sections non prises en charge. Le comparateur exact ne retire aucun espace. EXPECTF prend en charge les six jokers demandés, ainsi que %S, %a, %A, %w, %c, %0 et les blocs %r dans la syntaxe RE2. Les motifs portent sur toute la sortie, en mode octet.

L'exécuteur lance chaque interpréteur avec les arguments -n et le chemin du programme. Il copie le répertoire du test, restaure cette copie au même chemin avant la cible et supprime la copie à la sortie. FILE_EXTERNAL doit rester dans ce répertoire. Les liens symboliques et fichiers spéciaux sont refusés. SKIPIF est exécuté avant FILE pour chaque interpréteur ; une divergence de décision ou de sortie ne peut devenir une omission silencieuse.

Le mode différentiel compare stdout, stderr et le code de sortie indépendamment, puis exige également la conformité de stdout à l'attendu PHPT. Le mode autonome exige la conformité de stdout, un stderr vide et un code nul, car les sections demandées ne définissent pas d'attendus séparés pour ces deux observations. Le délai vaut dix secondes par processus par défaut ; --timeout le configure. Chaque flux est borné à 8 Mio ; son dépassement produit une erreur, jamais une comparaison tronquée. Le groupe de processus reçoit SIGKILL lors de l'annulation et à la fin du processus principal. Les diagnostics du lanceur indiquent explicitement lorsqu'ils ne montrent que les 4096 premiers octets ; les comparaisons utilisent les sorties complètes acceptées.

Le lanceur parcourt les dossiers récursivement, déduplique les chemins et poursuit les autres cas après une erreur de test. Il affiche PASS, FAIL, SKIP et ERROR avec leurs totaux. Le code 0 signifie qu'aucun échec ni aucune erreur n'a été rencontré, sans assimiler SKIP à PASS ; le code 1 signale un échec et le code 2 une erreur d'entrée ou d'exécution. Une sélection vide produit le code 2.

## Mesures

Les commandes suivantes ont été exécutées depuis /devhoros/php127, sauf indication contraire.

| Commande | Code | Observation |
| --- | --- | --- |
| `go version` | 0 | L'outil annonce go1.27.2 linux/amd64. |
| `/usr/bin/php --version` | 0 | L'oracle annonce PHP 8.3.6 CLI. |
| `go test ./...` avant implémentation | 0 | Les paquets types et zone passent. |
| `go test -run 'Test(Parse|Match|Run|Differential)' ./testing/phpt` avant implémentation | 1 | Les fonctions demandées sont encore absentes ; ce constat est une preuve d'absence, pas une mutation comportementale. |
| `go test -run TestMatchStrictBytesAndFormats ./testing/phpt` avant correction des jokers | 1 | Les cas de pourcentages, %0 et flottant terminé par un point échouent. |
| `go test -run TestParsePreservesNonSectionDelimiterLines ./testing/phpt` avant correction du délimitage | 1 | Une ligne littérale entre doubles tirets est prise à tort pour une section. |
| `go test -run 'Test(Parse|Match|Run|Differential|Execute)' ./testing/phpt` avant correction du tampon | 1 | La promotion de bytes.Buffer.ReadFrom contourne la limite de sortie ; le champ nommé supprime ce contournement. Le test a ensuite été nommé TestExecuteReportsSignalAndOutputLimit. |
| `go test -run 'TestMatchStrictBytesAndFormats\|TestOfficialPHPTDetectsTrailingNewlineMismatch' ./testing/phpt` avec mutation TrimSpace | 1 | Les différences d'espaces, de CRLF et de fin de ligne font échouer les garanties. La mutation a été retirée. |
| `go test -race -count=1 ./testing/phpt/...` | 0 | La commande demandée passe. |
| `go test -run TestCLI ./cmd/phpt-runner` | 0 | Les deux modes, les verdicts, les codes, la sélection vide et le parcours récursif sont vérifiés. |
| `go test -race -count=3 ./...` après retrait de la mutation | 0 | Les quatre paquets du module passent trois fois. |
| `go vet ./testing/phpt/... ./cmd/phpt-runner/...` | 0 | Aucun diagnostic n'est émis. |
| `go fix -diff ./testing/phpt/... ./cmd/phpt-runner/...` | 0 | La sortie est vide. |
| `go build -o /dev/null ./cmd/phpt-runner` | 0 | Le lanceur compile sans laisser de binaire dans le dépôt. |
| `go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./testing/phpt ./cmd/phpt-runner` | 0 | Seuls les deux paquets du lot apparaissent hors bibliothèque standard. |

Les épreuves d'exécution appellent le véritable /usr/bin/php. Les scripts qui injectent une divergence de stdout, de stderr ou de code de sortie servent uniquement de mutations d'épreuve ; aucun résultat PHP n'est simulé dans le runner. Le test d'échéance utilise testing/synctest avec un contexte déjà expiré ; il ne prétend pas prouver à lui seul l'arrêt d'un descendant actif. Une mesure initiale avec un programme PHP en boucle et un délai de 1 ms a également retourné l'erreur d'échéance attendue pendant la passe.

## Épreuve officielle

Le fichier /devhoros/php127/testing/phpt/testdata/php-src-8.3.6/001.phpt provient de https://raw.githubusercontent.com/php/php-src/php-8.3.6/tests/lang/001.phpt. La commande `curl --fail --silent --show-error https://raw.githubusercontent.com/php/php-src/php-8.3.6/tests/lang/001.phpt | sha256sum` et la commande `sha256sum testing/phpt/testdata/php-src-8.3.6/001.phpt` produisent toutes deux 14a5aa607a33beab0a35ab1551958b3a32ee5440db4878367b3e9186ae239128, avec le code 0.

La commande `go run ./cmd/phpt-runner --oracle /usr/bin/php --target /usr/bin/php ./testing/phpt/testdata/php-src-8.3.6` produit TOTAL=1 PASS=0 FAIL=1 SKIP=0 ERROR=0 et se termine avec le code 1. Les deux interpréteurs produisent exactement les mêmes trois observations, mais stdout contient Yes sans fin de ligne alors que EXPECT contient Yes suivi de LF. Ce refus applique la règle stricte demandée, contrairement aux normalisations du runner officiel. Le fichier n'a pas été adapté pour obtenir PASS.

La commande `go run ./cmd/phpt-runner --target /usr/bin/php --timeout 1ns ./testing/phpt/testdata/php-src-8.3.6/001.phpt` produit ERROR et context deadline exceeded. Le lanceur retourne 2 ; go run affiche exit status 2 et retourne lui-même 1.

La commande `sha256sum /usr/bin/php` retourne le code 0 et l'empreinte 1c564e6bafa56d03359c3774be6f4c77d0edecabd2075e18556940a56747c8d7. Cette empreinte documente l'oracle mesuré ; le runner ne verrouille pas arbitrairement le binaire configurable par --oracle.

## Limites et livraison

EXPECTREGEX et les blocs %r utilisent le moteur RE2 de Go, pas PCRE2. Les constructions non reconnues par RE2, notamment les références arrière et les assertions de voisinage, produisent une erreur. La compatibilité sémantique complète des regex PHP n'est pas réalisée et aucune validation exhaustive du corpus php-src n'est revendiquée.

Les sections INI, ENV, CLEAN, ARGS, STDIN, EXTENSIONS, les variantes CGI et les autres sections hors ordre sont refusées, jamais ignorées. Les dépendances de fixtures situées au-dessus du répertoire du test ne sont pas copiées. Les sorties contenant des valeurs intrinsèquement variables, comme des identifiants de processus, restent des divergences exactes même si EXPECTF les accepterait.

L'isolation couvre les processus et les fichiers relatifs de travail ; elle n'interdit pas au programme PHP d'accéder au réseau ou aux chemins absolus de l'hôte. L'implémentation de groupes de processus vise Linux/Unix et n'a pas été qualifiée pour Windows. Le binaire /devhoros/php127/bin/php127 n'étant pas présent dans l'inventaire initial, aucune parité de ce moteur n'est revendiquée : les épreuves utilisent PHP contre PHP et des injections de divergences.

Le dépôt partagé contenait déjà des modifications et le module php127 n'était pas suivi dans HEAD. Aucun commit, aucune délégation, aucune couture et aucune modification de liste ou de budget de contrat n'ont été effectués. La preuve par git archive HEAD n'est donc pas disponible ; les mutations ont été mesurées dans l'arbre de travail puis retirées. Le manifeste local /devhoros/php127/testing/phpt/lot0.2.manifest.json énumère les fichiers et les mesures, sans se présenter comme un manifeste produit par horos porte.

## Ordre de reprise après audit Opus 5.5, tentative 2

La reprise du 13 octobre 2026 traite B1, M1, M2, M3, M4 et m2 de `/devhoros/php127/docs/audit_lot0_2_opus.md`. Les sections précédentes conservent les observations historiques de la tentative 1 ; les mécanismes et les mesures de cette section les remplacent pour la livraison courante. Le périmètre comprend les trois fichiers de production demandés, les deux fichiers de tests associés et ce rapport, soit six fichiers. Le suivi figure dans `/devhoros/php127/testing/phpt/reprise_lot0_2.work`.

### Mécanismes corrigés

La validation syntaxique B1 analysait chaque bloc `%r` isolément, puis le motif complet, avec `regexp/syntax.Parse`, avant l'ajout des ancres. Le test `TestMatchRejectsAnchorInjection` reprend les deux injections de l'audit et ajoute deux blocs déséquilibrés. Contrairement à la formulation initiale de ce rapport, ces deux blocs ne se compensent pas après concaténation. La reprise 2 ci-dessous retire la validation isolée redondante et conserve la validation complète avant ancrage.

Le repli de casse M4 conserve les octets ASCII et représente les octets 128 à 255 par des caractères à usage privé sans paire de casse Unicode. La traduction porte sur les littéraux, les échappements hexadécimaux et octaux, les zones citées et les classes de caractères. Les classes sont projetées sur l'alphabet des 256 octets avant traduction : une plage traversant la frontière ASCII ne peut ainsi introduire indirectement les replis Unicode de Kelvin ou du s long. Les négations restent des négations afin que `(?i)[^a]` exclue aussi `A`. Les propriétés Unicode restent interprétées sur l'alphabet des octets ; cette convention ne constitue pas une compatibilité PCRE complète.

L'isolation M1 conserve la racine créée par `os.MkdirTemp` pendant les deux exécutions. Le nettoyage ne retire que son contenu ; ses droits sont remis à 0700 avant chaque préparation. `TestRunKeepsPrivateWorkspace` mesure ces droits depuis le véritable PHP, puis les change volontairement à 0777 : la cible doit néanmoins retrouver 0700. La suppression de la racine n'intervient qu'au nettoyage final.

La copie M3 ne parcourt plus récursivement l'arbre source. Elle sélectionne les fichiers directement voisins du test, sans les autres fichiers `.phpt`, et les fichiers directement voisins du programme explicitement nommé par `FILE_EXTERNAL` lorsque celui-ci réside dans un sous-répertoire. Les fichiers sont copiés par flux depuis une racine ouverte avec `os.OpenRoot`, sans charger leur contenu entier en mémoire. Les liens symboliques et fichiers spéciaux rencontrés dans les répertoires sélectionnés restent refusés. `TestRunCopiesOnlyLocalFixtures` prouve l'absence de copie du sous-dossier non lié et des autres tests ; `TestRunCopiesNestedExternalFixtures` garde le fonctionnement d'un programme externe imbriqué et le refus d'un lien symbolique à sa place.

L'épreuve M2 lance réellement un parent PHP et un enfant PHP, attend une échéance de 500 ms et exige deux identifiants de processus observés, `context.DeadlineExceeded`, une durée inférieure à trois secondes et l'absence de processus encore actif. Un zombie est un processus arrêté en attente de récupération par l'hôte, pas un descendant actif. La disparition concurrente de `/proc/<pid>/stat` peut produire `ENOENT` ou `ESRCH` ; ces deux erreurs sont traitées comme une disparition. Une seconde épreuve détache volontairement un descendant avec `/usr/bin/setsid`, conserve ses tubes ouverts et vérifie `exec.ErrWaitDelay` en moins de trois secondes. Le test arrête explicitement ce descendant après la mesure. Ces deux épreuves utilisent le temps réel, car les entrées-sorties de processus externes ne relèvent pas de l'horloge virtuelle de `synctest` ; l'ancien test de contexte pré-expiré est conservé pour son seul objet.

L'arrêt m2 vérifie l'annulation pendant la découverte et avant chaque nouveau test. Le compteur `TOTAL` porte désormais sur les tests effectivement abordés, et l'annulation impose le code 2. `TestCLIStopsOnCancellation` annule le contexte lors du premier diagnostic : le second fichier ne doit apparaître ni dans les diagnostics ni dans les totaux. Cette épreuve porte sur le contexte alimenté par le gestionnaire SIGINT existant ; elle ne prétend pas envoyer elle-même un signal système.

### Preuves rouges puis vertes

Les commandes sont exécutées depuis `/devhoros/php127`, sauf la lecture Git exécutée depuis `/devhoros`. Les tests ont été ajoutés avant les correctifs de production.

| Commande et état | Code | Observation mesurée |
| --- | --- | --- |
| `go test ./...`, à l'ouverture | 1 | Un cycle d'importation affecte `zone` et `TestStringNumericBoundaries` échoue dans `types`. Les deux paquets du lot passent. Aucun fichier de ces paquets voisins n'a été modifié par cette reprise. |
| `go test -count=1 -run 'TestMatchRejectsAnchorInjection\|TestMatchASCIICaseFolding\|TestRunKeepsPrivateWorkspace\|TestRunCopiesOnlyLocalFixtures\|TestExecuteActiveDeadlineKillsGroup' ./testing/phpt`, avant correctif | 1 | Les quatre premières épreuves échouent : injections acceptées, casse non-ASCII repliée, droits 775 et fichiers non liés copiés. L'épreuve d'échéance active passe déjà, conformément au mécanisme de production existant. |
| `go test -count=1 -run TestCLIStopsOnCancellation ./cmd/phpt-runner`, avant correctif | 1 | Le lanceur affiche deux erreurs et `TOTAL=2` après l'annulation au premier diagnostic. |
| `go test -count=1 -run 'TestMatch\|TestRun\|TestExecute\|TestDifferential' ./testing/phpt`, après correctif | 0 | Les injections, les droits, la copie locale, la casse et les processus satisfont leurs assertions. |
| `go test -count=1 -run TestCLI ./cmd/phpt-runner`, après correctif | 0 | L'annulation laisse `TOTAL=1` et ne traite pas le second fichier. |
| `go test -count=1 -run TestMatchASCIICaseFolding ./testing/phpt`, après ajout du cas de relecture `[^^]` | 1 | La suppression textuelle de la négation altérait le caret littéral. La classe originale est désormais conservée et son ensemble d'octets inversé ; les rejeux finaux couvrent ces cas. |
| `go test -count=1 -run TestExecuteActiveDeadlineKillsGroup ./testing/phpt`, avec `Setpgid` désactivé, annulation du seul parent et suppression du nettoyage de groupe | 1 | Un enfant PHP demeure actif après 1,50 s ; le nettoyage de l'épreuve l'arrête. |
| `go test -count=1 -run TestExecuteBoundsInheritedPipes ./testing/phpt`, avec `WaitDelay=0` | 1 | L'exécution dure 4,012 s et ne retourne aucune erreur, au lieu de signaler le délai des tubes hérités. |
| `go test -count=1 -run TestExecuteActiveDeadlineKillsGroup ./testing/phpt`, sans contrôle final de `ctx.Err()` | 1 | L'erreur devient `process execution: signal: killed`, non reconnaissable comme `context.DeadlineExceeded`. |
| `go test -race -count=3 ./...`, premier rejeu après retrait des mutations | 1 | La lecture de `/proc` rencontre `ESRCH` deux fois. Le test ne reconnaissait que `ENOENT` ; sa vérification est corrigée sans toucher au dépilement. |
| `go test -race -count=1 ./testing/phpt/... ./cmd/phpt-runner/...`, après correction de l'épreuve | 0 | Les deux paquets passent avec le détecteur de courses. |
| `go test -race -count=3 ./...`, rejeu final | 0 | Les quatre paquets passent trois fois. Les défauts voisins présents à l'ouverture ne sont plus observés ; leur résolution ne relève pas de cette reprise. |
| `go vet ./...` | 0 | Aucun diagnostic n'est émis. |
| `go fix -diff ./testing/phpt/... ./cmd/phpt-runner/...` | 0 | La sortie est vide après application des deux suggestions idiomatiques initiales. |
| `go version` | 0 | L'outil annonce `go1.27.2 linux/amd64`. |
| `/usr/bin/php --version` | 0 | L'oracle annonce PHP 8.3.6 CLI. |
| `git ls-files -- php127`, depuis `/devhoros` | 0 | La sortie est vide : le module n'est pas suivi dans HEAD. |

La confrontation directe avec PHP a également retourné le code 0 et les sorties `bool(false)` puis `int(0)` pour la commande suivante :

```sh
/usr/bin/php -n -r '$bad = @preg_match("~attendu)|(?s:.*~s", "SORTIE TOTALEMENT FAUSSE"); $fold = preg_match("~(?i)\\xE9~", "\xC9"); var_dump($bad, $fold); exit($bad === false && $fold === 0 ? 0 : 1);'
```

### Limites de la reprise

Les trois mutations de dépilement ont été retirées avant les mesures finales. Aucun commit, aucune délégation, aucune couture et aucune modification de liste ou de budget n'ont été effectués. La preuve rouge puis verte est une mesure de l'arbre de travail ; elle ne remplace pas le rejeu par `git archive HEAD`, toujours impossible pour ce module non suivi. Le manifeste `/devhoros/php127/testing/phpt/reprise_lot0_2.manifest.json` décrit cette livraison locale, sans prétendre être produit par la porte.

La sélection des fichiers voisins répond au périmètre de copie autorisé par l'ordre. Elle ne découvre pas les dépendances PHP calculées dynamiquement dans d'autres sous-répertoires : celles-ci ne sont pas copiées et leur absence reste observable à l'exécution. La lecture des noms du répertoire demeure nécessaire pour chaque test ; aucun temps linéaire de bout en bout ni aucune accélération chiffrée sur une grande suite php-src ne sont revendiqués. Les descendants qui changent de session restent hors de la garantie d'arrêt du groupe. Les mineurs m1 et m3 de l'audit ne font pas partie de l'ordre de reprise et ne sont pas déclarés corrigés.

## Ordre de reprise 2, tentative 3

La reprise traite R1, R2, R3, R4 et m1 de `/devhoros/php127/docs/audit_lot0_2_opus_reprise.md`. Le rapport détaillé, avec les commandes et leurs codes de sortie, figure dans `/devhoros/php127/docs/rapport_lot0_2_reprise2_astra.md`. Le suivi est conservé dans `/devhoros/php127/docs/lot0_2_reprise2.work` et le manifeste local dans `/devhoros/php127/docs/lot0_2_reprise2.manifest.json`.

La projection R1 traduit les six classes Perl sur les octets, y compris dans les unions et négations entre crochets. Les blancs comprennent les octets 09 à 0d et 20. Les propriétés Unicode sont soustraites au repli de casse ; dans une classe mixte, seuls les membres ordinaires sont repliés, puis les propriétés sont réunies et la négation éventuelle appliquée. La table différentielle compare 27 motifs sur les 256 octets, soit 6912 observations de correspondance, au véritable `/usr/bin/php`. Les variantes avec propriétés entre crochets ont également rougi avant leur correction.

La garde R2 lance un enfant PHP dont les trois descripteurs sont redirigés vers `/dev/null`, attend sa publication de PID, puis exige la sortie normale du parent en moins de trois secondes. Le retrait du seul nettoyage différé fait échouer `TestExecuteNormalExitKillsGroup` avec un enfant encore actif après 200 ms ; la remise en place le fait passer. Le nettoyage du test arrête le survivant même sous mutation. `/devhoros/php127/testing/phpt/runner.go` ne conserve aucune modification de production.

La sonde R3 accepte les états X et Z ainsi que les disparitions ENOENT et ESRCH. Elle relit les autres états toutes les 5 ms jusqu'à une borne de 200 ms. Les tests de sortie normale et d'échéance partagent cette sonde. Vingt répétitions avec le détecteur de courses, sous seize boucles de charge CPU et une limite mémoire de 8 Go, passent avec le code 0. Cette mesure ne constitue pas une reproduction déterministe du faux rouge antérieur.

La validation isolée R4 est supprimée. `TestMatchCompensatedRawBlocks` vérifie le véritable cas compensé `%r(%r%r)%r`, refusé avant cette suppression ; les injections restent refusées par la validation complète avant ancrage. `TestMatchWhitespaceVerticalTab` accepte 0x0b et devient rouge sous une mutation de `%w` vers la sémantique RE2 brute. Un remplacement textuel par `\s*` qui conserve la projection R1 n'est plus une mutation sémantique : le rapport ne le présente pas comme une régression détectable.

Les commandes `go test -race -count=3 ./testing/phpt/... ./cmd/phpt-runner/...`, `go vet ./testing/phpt/... ./cmd/phpt-runner/...`, `go test -race -count=3 ./...` et `go fix -diff ./testing/phpt/... ./cmd/phpt-runner/...`, exécutées depuis `/devhoros/php127`, retournent toutes le code 0. La dernière commande n'émet aucun changement. Aucun commit, aucune délégation, aucune modification de liste ou de budget et aucune couture n'ont eu lieu. Le module reste non suivi dans HEAD ; les preuves de mutation sont locales et ne valent pas rejeu sur archive Git.

## Ordre de reprise 3, tentative 4

La reprise traite N1, N2, N3 et N4 de `/devhoros/php127/docs/audit_lot0_2_opus_reprise2.md`. Le rapport courant, rédigé avec ses commandes et codes de sortie complets, est `/devhoros/php127/docs/rapport_lot0_2_reprise3_astra.md`. Il remplace les descriptions antérieures de la projection et de la traduction EXPECTF, sans transformer les mesures historiques en mesures nouvelles.

La projection des classes traduit désormais `\v` par `\x0a-\x0d\x85`, tandis que `%w` conserve exactement `\x09-\x0d\x20`. Les classes POSIX `upper` et `lower` deviennent `alpha` sous repli de casse, y compris lorsque leur négation est interne. Le repli ASCII précède la négation extérieure ; toutes les classes émises sont protégées par `(?-i:...)`. Les propriétés Unicode restent indépendantes du repli. Une garde commune refuse les treize écritures récentes explicitement désignées par l'ordre, dans les propriétés isolées comme entre crochets.

La traduction EXPECTF échappe d'abord les portions littérales et encadre les blocs `%r`, puis remplace les douze jokers dans toute l'expression. Le pourcentage doublé n'est pas un échappement : `%%` reste littéral et `%%d` traduit le second pourcentage avec `%d`. La fonction officielle de PHP 8.3.6 confirme les 19 cas permanents de `TestMatchRawFormatWildcards`, avec le code 0.

La commande `go test -count=1 -run 'TestMatchByteClassesAgainstPHP\|TestMatchRawFormatWildcards\|TestMatchRejectsRecentUnicodeScripts' ./testing/phpt` a retourné le code 1 avant correction. Le rejeu `go test -count=1 -run TestMatch ./testing/phpt` a ensuite retourné le code 0. La table des classes comprend 117 motifs sur 256 octets, dont les 84 combinaisons POSIX demandées. La garde des treize écritures est vérifiée sous quatre formes et dans les deux modes d'attente.

La sonde complète originale de l'auditeur a traité ses 9 330 motifs, sans réduction de périmètre. Le bilan `/devhoros/php127/docs/lot0_2_reprise3_differentiel.tsv` comporte 2 966 272 observations, zéro faux PASS, zéro PHP-ERREUR-RUNNER-PASS et zéro refus strict de correspondance. Les 546 motifs refusés par les deux moteurs et les deux syntaxes refusées uniquement par RE2 ne sont pas comptés comme observations de correspondance. La sonde retourne le code 0, et son bilan a été lu séparément puisque son test n'impose pas lui-même d'assertion globale.

Les commandes finales `go test -race -count=3 ./testing/phpt/... ./cmd/phpt-runner/...`, `go vet ./testing/phpt/... ./cmd/phpt-runner/...`, `go test -race -count=3 ./...` et `go fix -diff ./testing/phpt/... ./cmd/phpt-runner/...`, exécutées depuis `/devhoros/php127`, retournent toutes le code 0. La dernière commande n'émet aucun changement. `/devhoros/php127/testing/phpt/runner.go` reste inchangé. Aucun commit ni aucune délégation n'ont eu lieu ; le rejeu sur archive Git reste impossible pour le module non suivi.

Les quatre fichiers historiques `lot0.2.work`, `reprise_lot0_2.work`, `lot0.2.manifest.json` et `reprise_lot0_2.manifest.json` résident désormais dans `/devhoros/php127/docs/`, sans modification de contenu. Les chemins anciens cités dans les sections historiques décrivent donc leur emplacement à l'époque, pas un résidu actuel. Aucun fichier de suivi ni manifeste ne demeure dans `/devhoros/php127/testing/phpt/`.

## Ordre de reprise 4, tentative 5

La reprise traite M1, M2, M3 et m1 de `/devhoros/php127/docs/audit_lot0_2_opus_reprise3.md`. Le rapport détaillé `/devhoros/php127/docs/rapport_lot0_2_reprise4_astra.md` contient les commandes, leurs codes et les limites. Les accents des lignes 1 à 57 sont restaurés sans modifier les observations historiques.

Les jokers EXPECTF reprennent les substitutions textuelles de PHP ; les blocs bruts restent groupés et le drapeau `(?-s)` conserve sa portée locale. Le flottant `%f` est refusé dans les contextes où sa réécriture sans assertion avant n'est pas équivalente : classe, échappement, quantificateur suivant ou citation littérale. Le séparateur `%e` précédé d'une barre oblique inverse dans un bloc brut est également refusé, car l'échappement du délimiteur PHP ne se transpose pas textuellement à RE2 dans ce contexte.

La lecture numérique commune refuse les valeurs hors octet dans les classes comme hors des classes et les références arrière potentielles hors classes. Elle conserve la limite de trois chiffres octaux effectivement mesurée sur PHP : `\0400` reste un espace suivi de `0`. Le marqueur interne des propriétés est désormais l'ensemble vide `\P{Any}`, sans exception à la borne de 255. La fermeture POSIX est recherchée après `[:` et seuls les quatorze noms demandés reçoivent ce traitement ; les fragments de crochets littéraux ne provoquent plus de panique.

Les nouveaux tests ont reproduit les faux PASS et la panique avant correction. La table permanente confronte 1 060 cas à `/usr/bin/php` 8.3.6, dont les 45 cas de l'audit, la matrice de douze jokers dans sept contextes sur douze sorties et les cas de portée et de flottant supplémentaires. Elle distingue explicitement l'ancre officielle `$` de l'ancre stricte `\z` : un seul cas de LF final diverge entre les deux références. Les références arrière et les constructions non prises en charge doivent retourner une erreur, jamais un PASS.

La sonde complète conserve les 9 330 motifs et donne 2 966 272 observations, `faux_pass=0`, `php_erreur_runner_pass=0` et `refus_stricts=0`. Les deux refus propres à RE2 restent `\C` et `[\Q]\E]`. Le bilan est conservé dans `/devhoros/php127/docs/lot0_2_reprise4_differentiel.tsv`. Le rejeu séparé des 45 cas ne donne aucun faux PASS ni aucune panique ; ses deux refus stricts sont le LF final et une référence arrière reconnue par PHP.

Les commandes `go test -race -count=3 ./testing/phpt/... ./cmd/phpt-runner/...`, `go vet ./testing/phpt/... ./cmd/phpt-runner/...` et `go fix -diff ./testing/phpt/... ./cmd/phpt-runner/...` retournent le code 0 depuis `/devhoros/php127`, avec une sortie vide pour la dernière. Les commandes `go test -race -count=3 ./...` et `go test -fuzz=Fuzz -fuzztime=5s ./testing/phpt`, chacune exécutée sous `systemd-run --user --scope -p MemoryMax=8G`, retournent également le code 0. Le dernier fuzzing réalise 160 196 exécutions sans panique. Aucun fichier de production extérieur au comparateur n'a été modifié ; la preuve sur archive Git demeure indisponible.

## Ordre de reprise 5, tentative 6

La reprise traite N1, N2 et m2 de `/devhoros/php127/docs/audit_lot0_2_opus_reprise4.md`, sur l'ordre explicite de l'usager. Le rapport détaillé est `/devhoros/php127/docs/rapport_lot0_2_reprise5_astra.md`. Le contrôle du délimiteur intervient après la substitution des jokers : les portions littérales et `%e` échappent `/`, tandis que les barres obliques non échappées des expressions brutes sont refusées. PHP 8.3.6 impose cette règle même dans les classes et dans `\Q...\E`.

La validation des classes précède leur projection sur les octets. Elle refuse les classes abrégées ou POSIX comme bornes d'intervalle, les classes nommées POSIX hors classe englobante et les constructions de collation ou d'équivalence non prises en charge par PCRE. Les tirets initiaux, finaux ou échappés restent littéraux. Les formes `[.a.]` et `[=a=]` sont également refusées hors classe englobante, conformément à l'oracle mesuré.

La réécriture de `%f` sans assertion avant n'est pas une équivalence générale. Devant `.\d`, notamment `%f.5` sur `.5` ou `+.5`, et `%r%f\.5%r` sur `.5`, elle constitue un refus strict : le langage accepté dans ces contextes reste inclus dans celui de PHP, sans faux PASS. Ces trois différences sont vérifiées et documentées ; le comportement n'est pas modifié pour le mineur m2. Cette précision remplace l'affirmation d'équivalence du rapport de reprise 4.

`TestMatchReprise5AgainstPHP` reprend les 53 lignes des deux sondes et les complète par une matrice, pour 965 cas. Le test retourne le code 1 avant le correctif puis le code 0 après celui-ci. `TestR3` rejoue les 45 cas antérieurs. `TestAuditDifferentiel` conserve les 9 330 motifs et vérifie les 2 966 272 observations avec chacun des délimiteurs `~` et `/` : zéro faux PASS, zéro refus strict de correspondance et aucun motif accepté par le runner lorsque PHP refuse sa compilation. Les deux refus propres à RE2 restent présents. Ces contrôles sont désormais permanents et comportent des assertions, contrairement aux anciennes sondes purement déclaratives.

Les commandes `go test -race -count=3 ./testing/phpt/... ./cmd/phpt-runner/...`, `go test -count=3 ./...`, `go vet ./testing/phpt/... ./cmd/phpt-runner/...` et `go fix -diff ./testing/phpt/... ./cmd/phpt-runner/...` retournent le code 0 depuis `/devhoros/php127`. La dernière sortie est vide. `go test -run '^$' -fuzz=FuzzMatch -fuzztime=5s ./testing/phpt` retourne le code 0 après 182 103 exécutions. Les suites et le fuzzing passent sous `systemd-run --user --scope -p MemoryMax=8G`. La première commande avec détection des courses a été interrompue par la limite de 120 secondes de l'outil ; son rejeu avec une limite de 600 secondes termine en 161,541 secondes pour le paquet PHPT. Aucun résultat de cette interruption n'est présenté comme une réussite.

## Ordre de reprise 6, tentative 7

La décision explicite de l'usager remplace le moteur RE2 par l'évaluation PCRE2 de `/usr/bin/php -n`, ou de l'oracle configuré. Le rapport `/devhoros/php127/docs/rapport_lot0_2_reprise6_astra.md` contient les preuves avant correction, les commandes finales et leurs limites. Les descriptions précédentes de RE2 sont historiques et ne décrivent plus le comparateur courant.

`EXPECT` conserve la comparaison brute en Go. Les deux autres formats passent par un processus PHP persistant protégé par un mutex, avec un protocole binaire à longueurs préfixées. La traduction EXPECTF conserve les captures et les douze substitutions de PHP ; aucun format ne normalise les fins de ligne. Les deux enveloppes de compilation sont contrôlées, puis la correspondance utilise `\A(?:...)\z`. Les erreurs de compilation et d'évaluation sont explicites. Le processus est réutilisé et redémarré après une panne de transport.

`TestMatchPCRECompilation` et `TestMatchPCRESemantics` retournent le code 1 avant remplacement, puis le code 0 après celui-ci. Les 232 observations des six fichiers de sondes et des 47 propriétés donnent zéro faux PASS et zéro refus de correspondance face à la référence stricte. Le bilan distingue un LF final, trois différences de compilation dues à l'enveloppe stricte (`\Qabc` et deux imbrications de profondeur 250), et un bloc brut non fermé explicitement refusé. Ces différences imposées empêchent d'affirmer que le LF est le seul écart avec le lanceur officiel.

Le banc des 9 330 motifs mesure désormais 2 966 784 observations par délimiteur, sans faux PASS, refus strict ni erreur propre au runner ; les 546 erreurs communes demeurent. Les anciennes restrictions RE2 sur les références arrière, les assertions avant et les flottants ont disparu. Les commandes `go test -race -count=3 ./testing/phpt/... ./cmd/phpt-runner/...`, `go vet ./testing/phpt/... ./cmd/phpt-runner/...` et `go fix -diff ./testing/phpt/... ./cmd/phpt-runner/...` retournent le code 0 ; la dernière sortie est vide. Le fuzzing de dix secondes retourne le code 0 après 525 588 exécutions. La suite complète passe trois fois avec les sondes externes activées par `PHPT_AUDIT_ROOT=/devhoros/.worktrees`. Les bancs lourds sont exécutés sous une limite mémoire de 8 Go.

Le banc `BenchmarkMatchPCRE` mesure 7 060 ns/op pour un motif EXPECTF répété. Les tests vérifient la réutilisation après 200 appels concurrents, le remplacement du processus tué et le chemin d'oracle configuré. Le délai fixe des tubes du comparateur reste distinct du contexte et du délai de `Run`. Le suivi et le manifeste locaux sont `/devhoros/php127/docs/reprise6.work` et `/devhoros/php127/docs/reprise6.manifest.json` ; aucune preuve par archive Git ni validation par la porte n'est revendiquée.

## Ordre de reprise 7, tentative 8

La reprise applique N7, l'arbitrage N8 et m1 à m5 sur ordre explicite de l'usager. Le rapport détaillé `/devhoros/php127/docs/rapport_lot0_2_reprise7_astra.md` consigne les commandes, leurs codes et les limites. Le suivi et le manifeste locaux sont `/devhoros/php127/docs/reprise7.work` et `/devhoros/php127/docs/reprise7.manifest.json`.

Le comparateur exige désormais que les enveloppes officielle brute et stricte acceptent toutes deux la sortie. Les six cas de récursion entière dans les deux formats ont échoué avant correction puis passent après celle-ci. Le lanceur conserve la comparaison séparée de stdout, stderr et du code de sortie ; après conformité à l'attendu, PASS exige désormais stderr vide et un code nul dans les deux modes. Cette règle remplace l'acceptation historique des erreurs identiques en mode différentiel. Elle ne reproduit pas la fusion des flux de `run-tests.php` et refuse donc les tests qui attendent un diagnostic sur stderr.

Les mutations de l'oracle final et de la validation avant SKIPIF font désormais échouer leurs épreuves. L'aide annonce PCRE2 via l'oracle PHP. L'enfant PHP reçoit SIGKILL du noyau à la mort de son parent ; le test réel de mort forcée échoue sans ce mécanisme et passe avec lui. Cette garantie ne couvre ni le nettoyage des répertoires après SIGKILL ni l'arrêt récursif de tous les descendants.

Les compteurs de faux PASS utilisent la forme officielle brute. Les 232 cas conservent zéro faux PASS. Le banc intégral conserve 9 330 motifs et 2 966 784 observations par délimiteur, avec zéro faux PASS et 256 refus stricts du motif `.$` sur des paires terminées par LF. Chaque résultat est également vérifié contre l'intersection des deux évaluations PHP ; aucun refus inexpliqué n'est toléré.

Les commandes `PHPT_AUDIT_ROOT=/devhoros/.worktrees systemd-run --user --scope -p MemoryMax=8G go test -race -count=3 ./testing/phpt/... ./cmd/phpt-runner/...`, `go vet ./testing/phpt/... ./cmd/phpt-runner/...` et `go fix -diff ./testing/phpt/... ./cmd/phpt-runner/...` retournent le code 0 depuis `/devhoros/php127`, avec une sortie vide pour la dernière. La suite complète `PHPT_AUDIT_ROOT=/devhoros/.worktrees systemd-run --user --scope -p MemoryMax=8G go test -count=3 ./...` retourne également le code 0. Les trois échecs initiaux de `types` ne se reproduisent plus, sans intervention de cette reprise dans ce paquet. Les fichiers réels `recursion.phpt` et `stderr_fusion.phpt` de l'audit donnent chacun FAIL au lanceur corrigé ; la commande de rejeu et le lanceur retournent le code 1 attendu. Aucun commit, aucune délégation, aucune modification de liste ou de budget et aucune couture n'ont eu lieu.

## Registre

La racine des ancres est `/devhoros/php127`. Les garanties de comparaison antérieures sont désormais portées par PCRE2, et non par les fonctions supprimées de projection RE2. Les mesures historiques restent dans les sections précédentes.

| classe | strate | ancre | mécanisme | exécutable | motif |
| --- | --- | --- | --- | --- | --- |
| B | S0 | testing/phpt/match.go:192 | Intersection officielle et stricte | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestMatchPCRERecursionEnvelope | Reprise 7, N7 : les six faux PASS initiaux sont refusés après correction. |
| B | S0 | testing/phpt/runner.go:236 | Refus du code non nul et de stderr non vide dans les deux modes | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestDifferentialRejectsMatchingNonzeroExitAndStderr | Reprise 7, N8 : la comparaison séparée demeure, sans fusion. |
| B | S0 | cmd/phpt-runner/main.go:38 | Aide PCRE2 via PHP | code.hazyhaar.fr/devhoros/php127/cmd/phpt-runner:TestCLIHelpNamesPCRE2 | Reprise 7, m2 : le test rougit avant correction et passe après. |
| B | S0 | testing/phpt/runner.go:296 | Arrêt de PHP à la mort du parent | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestExecuteParentDeathKillsPHP | Reprise 7, m3 : le PHP survivant est reproduit puis supprimé par Pdeathsig. |
| B | S0 | testing/phpt/phpt_test.go:197 | Validation avant SKIPIF | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestRunValidatesPatternBeforeSkip | Reprise 7, m4 : la suppression de la validation rend l'épreuve rouge. |
| B | S0 | testing/phpt/match.go:189 | Compilation effective des deux enveloppes PCRE2 | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestMatchPCRECompilation | Reprise 6, N3 à N6 : les acceptations indues reproduites avant correction sont refusées par PHP. |
| B | S0 | testing/phpt/match.go:175 | Captures et substitution officielle EXPECTF | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestMatchPCRESemantics | Les références arrière et les flottants passent après avoir rougi avant remplacement. |
| A | S0 | testing/phpt/match.go:33 | Comparaison exacte indépendante de PHP | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestMatchOracleErrorsAndBinaryFrames | EXPECT ne requiert aucun processus. |
| A | S0 | testing/phpt/match.go:38 | Refus préalable du bloc brut non fermé | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestMatchOracleErrorsAndBinaryFrames | Le diagnostic demeure explicite avant tout appel PCRE. |
| A | S0 | testing/phpt/match.go:189 | Validation avant évaluation strictement ancrée | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestMatchRejectsAnchorInjection | Les injections historiques restent refusées. |
| A | S0 | testing/phpt/match.go:192 | Sémantique des classes et des octets portée par PHP | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestMatchByteClassesAgainstPHP | Les projections RE2 sont supprimées, sans suppression des matrices de comparaison. |
| A | S0 | testing/phpt/match.go:47 | Sérialisation et réutilisation du processus | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestMatchWorkerLifecycle | Le test vérifie 200 appels concurrents puis le remplacement après arrêt forcé. |
| B | S0 | testing/phpt/runner.go:62 | Transmission de l'oracle configuré au comparateur | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestRunUsesConfiguredMatcherOracle | Reprise 7, m1 : la mutation de l'oracle final rend le test rouge. |
| A | S0 | testing/phpt/match.go:195 | Signalement des limites d'évaluation | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestMatchOracleErrorsAndBinaryFrames | Une erreur PCRE ne devient pas une non-correspondance silencieuse. |
| A | S0 | testing/phpt/match_pcre_test.go:241 | Rejeu des sept sources contre la référence officielle brute | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestMatchReprise6AuditFiles | Reprise 7, m5 : les 232 observations donnent zéro faux PASS. |
| A | S0 | testing/phpt/phpt_test.go:1140 | Confrontation intégrale des 9 330 motifs à la référence officielle brute | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestAuditDifferentiel | Reprise 7, m5 : chaque délimiteur donne 2 966 784 observations, zéro faux PASS et 256 refus expliqués par l'ancrage strict. |
| B | S0 | testing/phpt/runner.go:128 | Conservation du répertoire privé | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestRunKeepsPrivateWorkspace | La preuve historique des droits est conservée et le test est rejoué. |
| B | S0 | testing/phpt/runner.go:296 | Arrêt du groupe à échéance | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestExecuteActiveDeadlineKillsGroup | Les preuves historiques restent applicables ; le mécanisme est inchangé. |
| B | S0 | testing/phpt/runner.go:304 | Borne des tubes hérités | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestExecuteBoundsInheritedPipes | Le test de mutation historique demeure dans la suite. |
| B | S0 | testing/phpt/runner.go:100 | Copie des seuls fichiers locaux sélectionnés | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestRunCopiesOnlyLocalFixtures | La copie ne redevient pas récursive. |
| B | S0 | testing/phpt/runner.go:310 | Nettoyage du groupe après sortie normale | code.hazyhaar.fr/devhoros/php127/testing/phpt:TestExecuteNormalExitKillsGroup | Le mécanisme inchangé conserve sa preuve historique. |
| B | S0 | cmd/phpt-runner/main.go:102 | Interruption de la sélection restante | code.hazyhaar.fr/devhoros/php127/cmd/phpt-runner:TestCLIStopsOnCancellation | Le test historique est conservé et rejoué. |
| C | S0 | testing/phpt/rapport_lot0_2.md:57 | Rejeu sur archive versionnée | dette | Le module non suivi ne permet pas la preuve sur HEAD. |
| C | S0 | testing/phpt/match.go:116 | Annulation du comparateur et attente du mutex | dette | Le délai des tubes est fixe et distinct du contexte et du délai du runner. |
| C | S0 | testing/phpt/runner.go:38 | Isolation de sécurité | dette | Le répertoire privé et le groupe ne forment pas un bac à sable. |
| C | S0 | testing/phpt/runner.go:102 | Mesure des grandes sélections | dette | Les performances d'une grande suite réelle restent non qualifiées. |
| C | S0 | testing/phpt/runner.go:78 | Nettoyage après mort forcée | dette | SIGKILL empêche les nettoyages différés ; Pdeathsig ne tue pas récursivement les descendants. |
| C | S0 | testing/phpt/runner.go:236 | Attendus officiels portant sur stderr | dette | L'arbitrage N8 refuse stderr non vide et le code non nul, sans fusion des flux. |

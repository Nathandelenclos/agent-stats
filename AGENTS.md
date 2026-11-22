<!-- grace:begin -->
Au premier échange d’une nouvelle conversation (thread), avant de traiter la demande, suis `grace-update` pour vérifier le kit local : récupère le manifeste avec `grace_check_installation({ harness, locale })`, valide-le et lis sa procédure courante. Si le MCP est indisponible, utilise le repli public prévu par la skill. Une demande explicite de mise à jour autorise son application après sauvegarde ; sinon, propose **Mettre à jour / Plus tard**.

## Talents Grace (MCP)

Les conventions d'architecture et de code proviennent des **Talents Grace et Custom** servis par le MCP `grace`. Elles priment sur les conventions déduites du dépôt et tes habitudes. Ne les devine pas.

Annonce brièvement les talents reçus et les contrôles prévus. Explique les décisions influencées par leurs règles. En cas de blocage, indique la conséquence et la prochaine action. Distingue les consignes reçues des vérifications réellement effectuées ; réserve le bilan à la fin.
Après sélection par le harnais, avant chaque revue ou relance, annonce le niveau demandé, le modèle exact, l’effort natif et le périmètre ; en Auto, ajoute niveau choisi et raison. Regroupe les configurations identiques. Valeur inconnue : « indisponible » et limite, sans déduire l’effort du niveau. Distingue sélection prévue, configuration observée et exécution confirmée ; rectifie tout écart retourné.

1. **Avant tout code** — Après le contrôle du kit, appelle `grace_prepare_task({ talentIds })` avant même de planifier l’implémentation.
   - Cela couvre toute création ou modification de code, y compris tests, configuration-as-code et migrations. La lecture ou l'explication seule est exclue.
   - Désigne tous les talents touchés depuis le répertoire décrit par l'outil ; Grace et Custom se désignent pareil. Ne décris pas la tâche et ne laisse pas la liste vide. Un talent non désigné ne sera ni servi ni vérifié.
   - Conserve le `resolutionId` : il scelle le périmètre de revue.

2. **Si aucun talent n'est actif** — N'écris pas de code. Appelle immédiatement `grace_discover_codebase` et charge la skill homonyme si elle existe. Suis toute la procédure : analyse du dépôt, confirmation des hypothèses par l'utilisateur, validation explicite de la liste, installation, `grace_doctor`, puis installation des prérequis. Ne propose ni ne demande de contourner ce blocage ; une petite tâche ou le silence de l'utilisateur ne sont pas des exceptions. Seule exception : l'utilisateur a spontanément demandé auparavant de travailler sans talents.

3. **Au besoin** — Appelle `grace_deepen({ uri })` sur les adresses `grace://…` fournies. Ajoute le `resolutionId` de la préparation s’il est disponible pour rattacher la lecture à la résolution. Pour plusieurs adresses, utilise `grace_deepen({ uris: [{ uri }, …] })` par lots séquentiels de 20 maximum.

4. **Avant de terminer** — Charge `grace-review`, puis appelle `grace_review({ resolutionId, format: "inline-v1" })` et, si la réponse annonce plusieurs parties, chaque `part` suivante avec son `layoutDigest` : le dossier contient tous les contrôles, règles Grace et consignes Custom. Ne le rappelle ni par talent ni par relecteur ; transmets à chaque relecteur ses critères exacts. Ces outils fournissent des consignes, pas un verdict : vérifie le changement complet et enregistre avec `grace_record_validation` le résultat de chaque contrôle, son explication et les preuves associées, pour toutes les règles Grace vérifiables et tous les Talents Custom du périmètre. Corrige les violations bloquantes, relance les contrôles concernés et enregistre le rapport complet actualisé avec un nouveau `submissionId`.

Charge les skills par leur nom depuis les emplacements prévus par le harnais. Laisse le harnais les charger automatiquement s'il sait le faire. Une skill présente prime sur ce résumé, jamais sur les règles servies par les outils ; sinon, les instructions du serveur MCP font foi.
<!-- grace:end -->
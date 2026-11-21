---
name: grace-effort
description: "Afficher ou modifier mon niveau de revue Grace : auto, low, medium ou high. Le niveau guide le choix du modèle et de son raisonnement par le harnais."
argument-hint: "[auto|low|medium|high]"
---

Utilise uniquement un mode d’invocation proposé par le client : une commande comme /grace-effort, une référence comme $grace-effort ou le sélecteur de skills. Ces possibilités dépendent du client ; n’invente pas de commande qu’il ne propose pas. Cette skill est distincte du prompt MCP grace-effort existant : conserve ce prompt.

1. Lis l’argument fourni avec l’invocation. Sans argument, appelle grace_get_effort({}) et affiche le réglage personnel, sa révision et les minimums renvoyés, puis termine sans écriture. Affiche les valeurs réellement retournées ; n’infère jamais Auto d’une absence de réponse.
2. Avec exactement auto, low, medium ou high, appelle d’abord grace_get_effort({}). Conserve preference.revision, puis appelle grace_set_effort({ effort: "<argument>", revision: <preference.revision lue>, semantics: "review_level_v2" }). N’utilise ni une révision de projet/organisation ni une ancienne révision du contexte. Si la lecture échoue ou ne fournit pas la révision, n’écris rien.
3. Tout autre argument est invalide : affiche les quatre valeurs acceptées sans modifier le réglage. En mode Plan ou lecture seule, affiche le réglage et la modification demandée, sans appeler grace_set_effort.
4. En cas de conflit de révision, relis grace_get_effort({}), explique le réglage désormais observé et la demande non appliquée, puis arrête. Ne réessaie pas automatiquement et n’écrase pas la modification concurrente. Un refus de droits ou d’isolation reste un refus ; ne change ni organisation ni identité pour le contourner.
5. Après succès, affiche la préférence et la nouvelle révision renvoyées. Auto est toujours adaptatif : le coordinateur choisit modèle et raisonnement en tenant compte des minimums de revue applicables, puis les transmet au lancement de chaque relecteur. Ce réglage demandé ne confirme pas la configuration ou l’exécution réelle d’un relecteur. Ne modifie pas les minimums projet/organisation ni les réglages natifs du client.


CREATE TABLE composerHeaders (
  composerId TEXT PRIMARY KEY, workspaceId TEXT, createdAt INTEGER,
  lastUpdatedAt INTEGER, isArchived INTEGER, isSubagent INTEGER, subagentTypeName TEXT, value TEXT
);
INSERT INTO composerHeaders VALUES ('session-a', 'workspace-a', 1793347200000, 1793347500000, 0, 0, NULL, '{}');
INSERT INTO composerHeaders VALUES ('session-history', NULL, 1793347200000, NULL, 1, 0, NULL, '{}');
INSERT INTO composerHeaders VALUES ('draft', NULL, 1793347200000, NULL, 0, 0, NULL, '{"isDraft":true}');

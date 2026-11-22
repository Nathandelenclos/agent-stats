-- The second generation of the schema of OpenCode (tables session_v2, session_message and session_part), with invented content.
CREATE TABLE session_v2 (id text PRIMARY KEY, project_id text NOT NULL, parent_id text, fork_session_id text, slug text NOT NULL,
  directory text NOT NULL, title text, version text NOT NULL, time_created integer NOT NULL, time_updated integer NOT NULL);
CREATE TABLE session_message (id text PRIMARY KEY, session_id text NOT NULL, type text NOT NULL, seq integer NOT NULL,
  time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL);
CREATE TABLE session_part (message_id text NOT NULL, session_id text NOT NULL, ordinal integer NOT NULL, type text NOT NULL, call_id text,
  time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL, PRIMARY KEY (message_id, ordinal));

INSERT INTO session_v2 VALUES ('ses_a', 'prj', NULL, NULL, 'calm-river', '/work/demo-app', 'Refactor the loader', '2.0', 1767225600000, 1767225700000);
INSERT INTO session_v2 VALUES ('ses_b', 'prj', 'ses_a', NULL, 'quiet-lake', '/work/demo-app', NULL, '2.0', 1767225650000, 1767225660000);
INSERT INTO session_v2 VALUES ('ses_c', 'prj', NULL, 'ses_a', 'bold-fox', '/work/demo-app', 'Refactor the loader (fork)', '2.0', 1767226000000, 1767226100000);

-- ses_a: a prompt, a synthetic message, an answer whose tools are parts, an answer that holds its tool itself, a compaction
INSERT INTO session_message VALUES ('msg_a1', 'ses_a', 'user', 1, 1767225601000, 1767225601000, '{"time":{"created":1767225601000},"text":"Refactor the loader"}');
INSERT INTO session_message VALUES ('msg_a2', 'ses_a', 'synthetic', 2, 1767225601500, 1767225601500, '{"time":{"created":1767225601500},"text":"context the user did not type"}');
INSERT INTO session_message VALUES ('msg_a3', 'ses_a', 'assistant', 3, 1767225602000, 1767225612000, '{"time":{"created":1767225602000,"firstToken":1767225602800,"streamed":1767225608000,"completed":1767225612000},"agent":"build","model":{"id":"claude-test","providerID":"anthropic"},"cost":0.5,"tokens":{"input":200,"output":60,"reasoning":20,"cache":{"read":1000,"write":50}},"finish":"tool-calls","content":[]}');
INSERT INTO session_part VALUES ('msg_a3', 'ses_a', 0, 'text', NULL, 1767225603000, 1767225603000, '{"type":"text","text":"I will read the loader."}');
INSERT INTO session_part VALUES ('msg_a3', 'ses_a', 1, 'tool', 'call_1', 1767225604000, 1767225606000, '{"type":"tool","id":"call_1","name":"read","state":{"status":"completed","input":{"path":"loader.go"},"content":[{"type":"text","text":"package loader"}]},"time":{"created":1767225604000,"ran":1767225604100,"completed":1767225604600}}');
INSERT INTO session_part VALUES ('msg_a3', 'ses_a', 2, 'tool', 'call_2', 1767225605000, 1767225606000, '{"type":"tool","id":"call_2","name":"demo_search","state":{"status":"error","input":{"q":"x"},"error":{"type":"NotFound","message":"nothing"}},"time":{"created":1767225605000,"completed":1767225605300}}');
INSERT INTO session_message VALUES ('msg_a4', 'ses_a', 'assistant', 4, 1767225620000, 1767225630000, '{"time":{"created":1767225620000,"streamed":1767225625000,"completed":1767225630000},"model":{"id":"claude-test","providerID":"anthropic"},"cost":0,"tokens":{"input":30,"output":4,"reasoning":0,"cache":{"read":1200,"write":0}},"finish":"stop","error":{"type":"ProviderOverloaded","message":"busy"},"content":[{"type":"tool","id":"call_3","name":"write","state":{"status":"completed","input":{"path":"a.go"},"content":[{"type":"text","text":"ok"}]},"time":{"created":1767225621000,"ran":1767225621100,"completed":1767225622100}}]}');
INSERT INTO session_message VALUES ('msg_a5', 'ses_a', 'compaction', 5, 1767225640000, 1767225650000, '{"time":{"created":1767225640000},"status":"completed","reason":"auto","model":{"id":"claude-test","providerID":"anthropic"},"summary":"so far","cost":0.1,"tokens":{"input":500,"output":50,"reasoning":0,"cache":{"read":0,"write":0}}}');
INSERT INTO session_message VALUES ('msg_a6', 'ses_a', 'idle', 6, 1767225660000, 1767225660000, '{"time":{"created":1767225660000},"outcome":"succeeded"}');

-- ses_b: the subagent
INSERT INTO session_message VALUES ('msg_b1', 'ses_b', 'user', 1, 1767225651000, 1767225651000, '{"time":{"created":1767225651000},"text":"Find the callers"}');
INSERT INTO session_message VALUES ('msg_b2', 'ses_b', 'assistant', 2, 1767225652000, 1767225655000, '{"time":{"created":1767225652000,"firstToken":1767225652500,"streamed":1767225654000},"model":{"id":"gpt-test","providerID":"openai"},"cost":0.02,"tokens":{"input":20,"output":3,"reasoning":0,"cache":{"read":0,"write":0}},"finish":"stop","content":[]}');

-- ses_c: forked from ses_a: it holds a copy of the first messages, older than the fork
INSERT INTO session_message VALUES ('msg_c1', 'ses_c', 'user', 1, 1767225601000, 1767225601000, '{"time":{"created":1767225601000},"text":"Refactor the loader"}');
INSERT INTO session_message VALUES ('msg_c2', 'ses_c', 'assistant', 2, 1767225602000, 1767225612000, '{"time":{"created":1767225602000},"model":{"id":"claude-test","providerID":"anthropic"},"cost":0.5,"tokens":{"input":200,"output":60,"reasoning":20,"cache":{"read":1000,"write":50}},"finish":"stop","content":[]}');
INSERT INTO session_message VALUES ('msg_c3', 'ses_c', 'user', 3, 1767226001000, 1767226001000, '{"time":{"created":1767226001000},"text":"Do it with generics"}');
INSERT INTO session_message VALUES ('msg_c4', 'ses_c', 'assistant', 4, 1767226002000, 1767226010000, '{"time":{"created":1767226002000,"firstToken":1767226003000,"streamed":1767226009000},"model":{"id":"claude-test","providerID":"anthropic"},"cost":0.3,"tokens":{"input":80,"output":30,"reasoning":0,"cache":{"read":300,"write":0}},"finish":"stop","content":[]}');

-- The first generation of the schema of OpenCode and Kilo Code (tables session, message and part), with invented content.
-- Times are milliseconds: 1767225600000 is 2026-01-01T00:00:00Z.
CREATE TABLE session (id text PRIMARY KEY, project_id text NOT NULL, parent_id text, slug text NOT NULL, directory text NOT NULL,
  title text NOT NULL, version text NOT NULL, time_created integer NOT NULL, time_updated integer NOT NULL);
CREATE TABLE message (id text PRIMARY KEY, session_id text NOT NULL, time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL);
CREATE TABLE part (id text PRIMARY KEY, message_id text NOT NULL, session_id text NOT NULL, time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL);

INSERT INTO session VALUES ('ses_main', 'prj', NULL, 'calm-river', '/work/demo-app', 'Add a cache', '1.0', 1767225600000, 1767225660000);
INSERT INTO session VALUES ('ses_child', 'prj', 'ses_main', 'quiet-lake', '/work/demo-app', 'Look into the cache (subagent)', '1.0', 1767225620000, 1767225630000);
INSERT INTO session VALUES ('ses_fork', 'prj', NULL, 'bold-fox', '/work/demo-app', 'Add a cache (fork #1)', '1.0', 1767226000000, 1767226010000);
INSERT INTO session VALUES ('ses_empty', 'prj', NULL, 'idle-owl', '/work/demo-app', 'New session', '1.0', 1767225600000, 1767225600000);
INSERT INTO session VALUES ('ses_odd', 'prj', NULL, 'odd-one', '/work/' || char(27) || '[2Jdemo', 'Odd', '1.0', 1767225600000, 1767225600000);

-- ses_main: a prompt, an answer that calls two tools, a second answer, a failed one, and one still being written
INSERT INTO message VALUES ('msg_01', 'ses_main', 1767225601000, 1767225601000, '{"role":"user","time":{"created":1767225601000},"agent":"build","model":{"providerID":"anthropic","modelID":"claude-test"}}');
INSERT INTO part VALUES ('prt_01a', 'msg_01', 'ses_main', 1767225601000, 1767225601000, '{"type":"text","text":"Add a "}');
INSERT INTO part VALUES ('prt_01b', 'msg_01', 'ses_main', 1767225601000, 1767225601000, '{"type":"text","text":"cache"}');
INSERT INTO part VALUES ('prt_01c', 'msg_01', 'ses_main', 1767225601000, 1767225601000, '{"type":"text","text":"synthetic context","synthetic":true}');
INSERT INTO part VALUES ('prt_01d', 'msg_01', 'ses_main', 1767225601000, 1767225601000, '{"type":"text","text":"ignored text","ignored":true}');
INSERT INTO message VALUES ('msg_02', 'ses_main', 1767225602000, 1767225610000, '{"role":"assistant","time":{"created":1767225602000,"completed":1767225610000},"parentID":"msg_01","modelID":"claude-test","providerID":"anthropic","mode":"build","agent":"build","cost":0.25,"tokens":{"input":100,"output":40,"reasoning":10,"cache":{"read":600,"write":30}},"finish":"tool-calls"}');
INSERT INTO part VALUES ('prt_02a', 'msg_02', 'ses_main', 1767225603000, 1767225603000, '{"type":"step-start"}');
INSERT INTO part VALUES ('prt_02b', 'msg_02', 'ses_main', 1767225603000, 1767225605000, '{"type":"tool","callID":"call_1","tool":"bash","state":{"status":"completed","input":{"command":"ls"},"output":"a.go\nb.go\n","title":"ls","metadata":{},"time":{"start":1767225603000,"end":1767225604500}}}');
INSERT INTO part VALUES ('prt_02c', 'msg_02', 'ses_main', 1767225605000, 1767225606000, '{"type":"tool","callID":"call_1","tool":"demo_lookup","state":{"status":"error","input":{"q":"x"},"error":"not found","time":{"start":1767225605000,"end":1767225605200}}}');
INSERT INTO part VALUES ('prt_02d', 'msg_02', 'ses_main', 1767225609000, 1767225609000, '{"type":"step-finish","reason":"tool-calls","cost":0.25,"tokens":{"input":100,"output":40,"reasoning":10,"cache":{"read":600,"write":30}}}');
INSERT INTO message VALUES ('msg_03', 'ses_main', 1767225611000, 1767225620000, '{"role":"assistant","time":{"created":1767225611000,"completed":1767225620000},"parentID":"msg_01","modelID":"claude-test","providerID":"anthropic","cost":0,"tokens":{"input":50,"output":5,"reasoning":0,"cache":{"read":700,"write":0}},"finish":"stop"}');
INSERT INTO message VALUES ('msg_04', 'ses_main', 1767225621000, 1767225622000, '{"role":"assistant","time":{"created":1767225621000},"modelID":"claude-test","providerID":"anthropic","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"error":{"name":"APIError","data":{"message":"overloaded"}}}');
INSERT INTO message VALUES ('msg_05', 'ses_main', 1767225630000, 1767225630000, '{"role":"assistant","time":{"created":1767225630000},"modelID":"claude-test","providerID":"anthropic","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}}');

-- ses_child: the subagent of ses_main
INSERT INTO message VALUES ('msg_11', 'ses_child', 1767225621500, 1767225621500, '{"role":"user","time":{"created":1767225621500}}');
INSERT INTO part VALUES ('prt_11a', 'msg_11', 'ses_child', 1767225621500, 1767225621500, '{"type":"text","text":"Find where the cache goes"}');
INSERT INTO message VALUES ('msg_12', 'ses_child', 1767225622000, 1767225625000, '{"role":"assistant","time":{"created":1767225622000,"completed":1767225625000},"modelID":"gpt-test","providerID":"openai","cost":0.01,"tokens":{"input":10,"output":2,"reasoning":0,"cache":{"read":0,"write":0}},"finish":"stop"}');

-- ses_fork: a copy of the first prompt and answer of ses_main (they keep their times, older than the fork), then a prompt of its own
INSERT INTO message VALUES ('msg_21', 'ses_fork', 1767225601000, 1767225601000, '{"role":"user","time":{"created":1767225601000}}');
INSERT INTO part VALUES ('prt_21a', 'msg_21', 'ses_fork', 1767225601000, 1767225601000, '{"type":"text","text":"Add a cache"}');
INSERT INTO message VALUES ('msg_22', 'ses_fork', 1767225602000, 1767225610000, '{"role":"assistant","time":{"created":1767225602000,"completed":1767225610000},"modelID":"claude-test","providerID":"anthropic","cost":0.25,"tokens":{"input":100,"output":40,"reasoning":10,"cache":{"read":600,"write":30}},"finish":"stop"}');
INSERT INTO message VALUES ('msg_23', 'ses_fork', 1767226001000, 1767226001000, '{"role":"user","time":{"created":1767226001000}}');
INSERT INTO part VALUES ('prt_23a', 'msg_23', 'ses_fork', 1767226001000, 1767226001000, '{"type":"text","text":"Try another way"}');

-- ses_odd: values a hostile database could hold
INSERT INTO message VALUES ('msg_31', 'ses_odd', 1767225601000, 1767225601000, 'this is not json');
INSERT INTO message VALUES ('msg_32', 'ses_odd', 1767225602000, 1767225602000, '{"role":"assistant","modelID":"m","providerID":"p","cost":1,"tokens":{"input":-5,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}}');
INSERT INTO message VALUES ('msg_33', 'ses_odd', 1767225603000, 1767225603000, '{"role":"assistant","modelID":"m","providerID":"p","cost":-1,"tokens":{"input":5,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}}');
INSERT INTO message VALUES ('msg_34', 'ses_odd', 1767225604000, 1767225604000, '{"role":"assistant","modelID":"m","providerID":"p","cost":0,"tokens":{"input":5,"output":1,"reasoning":0,"cache":{"read":0,"write":0}},"error":{"name":"bad\u001b[2Jname"}}');
INSERT INTO message VALUES ('msg_35', 'ses_odd', 1767225605000, 1767225605000, '{"role":"assistant","modelID":"m","providerID":"p","cost":0,"tokens":{"input":9007199254740993,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}}');

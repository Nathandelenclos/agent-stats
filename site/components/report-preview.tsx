import { Activity, BarChart3, List, MessageSquare, Terminal } from "lucide-react";

const week = [
  { day: "Mon", codex: 86, claude: 54 },
  { day: "Tue", codex: 120, claude: 62 },
  { day: "Wed", codex: 95, claude: 73 },
  { day: "Thu", codex: 161, claude: 58 },
  { day: "Fri", codex: 131, claude: 100 },
  { day: "Sat", codex: 212, claude: 98 },
  { day: "Sun", codex: 115, claude: 115 },
];

export function ReportPreview() {
  return (
    <figure className="report-preview">
      <figcaption>
        <span><Terminal size={16} aria-hidden="true" /> agent-stats serve</span>
        <span>Sample data</span>
      </figcaption>
      <div className="report-window">
        <div className="report-sidebar" aria-hidden="true">
          <Activity size={20} />
          <span className="report-sidebar-active"><BarChart3 size={17} /></span>
          <span><List size={17} /></span>
          <span><MessageSquare size={16} /></span>
        </div>
        <div className="report-main">
          <div className="report-title"><strong>Usage overview</strong><span>Last 7 days</span></div>
          <dl className="report-metrics">
            <div><dt>Estimated cost</dt><dd>$14.80</dd></div>
            <div><dt>Requests</dt><dd>312</dd></div>
            <div><dt>Cache hit rate</dt><dd>76%</dd></div>
          </dl>
          <div className="report-chart">
            <p>Estimated cost by day</p>
            <div className="report-bars" aria-hidden="true">
              {week.map(({ day, codex, claude }) => (
                <div className="report-day" key={day}>
                  <div className="report-stack">
                    <span style={{ height: `${claude / 3.2}%` }} />
                    <span style={{ height: `${codex / 3.2}%` }} />
                  </div>
                  <span>{day}</span>
                </div>
              ))}
            </div>
            <ul className="sr-only">
              {week.map(({ day, codex, claude }) => (
                <li key={day}>{day}: Codex {(codex / 100).toFixed(2)} USD, Claude Code {(claude / 100).toFixed(2)} USD</li>
              ))}
            </ul>
          </div>
          <dl className="report-legend">
            <div><dt>Codex</dt><dd>$9.20</dd></div>
            <div><dt>Claude Code</dt><dd>$5.60</dd></div>
          </dl>
        </div>
      </div>
    </figure>
  );
}

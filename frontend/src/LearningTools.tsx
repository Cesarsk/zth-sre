import { useState } from 'react';

export function CommandBlock({ command }: { command: string }) {
  const [message, setMessage] = useState('');
  const copy = async () => {
    try { await navigator.clipboard.writeText(command); setMessage('Copied'); }
    catch { setMessage('Select the command and copy it manually.'); }
  };
  return <div className="command-block"><pre><code>{command}</code></pre><button className="secondary-button" onClick={() => void copy()}>Copy command</button><span role="status">{message}</span></div>;
}

export function InvestigationNotes({ id }: { id: string }) {
  const key = `sre-lab:notes:${id}`;
  const [failed, setFailed] = useState(false);
  const [value, setValue] = useState(() => { try { return localStorage.getItem(key) ?? ''; } catch { return ''; } });
  const save = (next: string) => {
    setValue(next);
    try { localStorage.setItem(key, next); setFailed(false); } catch { setFailed(true); }
  };
  return <section className="investigation-notes"><div className="notes-label"><label htmlFor="exercise-notes">Investigation notes</label><textarea id="exercise-notes" value={value} maxLength={16000} onChange={event => save(event.target.value)} placeholder="Observations, hypotheses, evidence, and next steps…" /></div><p className="phase-note" role="status">{failed ? 'Browser storage is unavailable. Copy your notes before leaving.' : 'Saved as you type in this browser, separately for each exercise.'}</p></section>;
}

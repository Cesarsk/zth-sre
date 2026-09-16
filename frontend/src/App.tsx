import { useEffect, useRef, useState } from 'react';
import { LabTerminal } from './LabTerminal';
import { ResourceDiagram, type DiagramData } from './ResourceDiagram';
import { LiveMetrics } from './LiveMetrics';
import exercises from './content/exercises.json';
import { CommandBlock, InvestigationNotes } from './LearningTools';

const names = ['api', 'dependency', 'prometheus', 'toolbox'] as const;
const scenarioExerciseIDs = ['cpu-saturation', 'useful-alerts', 'slo-burn-rate', 'vertical-horizontal', 'dependency-bottleneck', 'connection-pool', 'latency-slo', 'dns-failure', 'retry-storm', 'memory-leak', 'autoscaler-oscillation', 'file-forensics', 'blocked-traffic'];
const interventionLabels: Record<string, { action: string; label: string }> = {
  'dependency-bottleneck': { action: 'dependency-recovery', label: 'Restore dependency path' },
  'connection-pool': { action: 'pool-recovery', label: 'Restore connection pool' },
  'latency-slo': { action: 'latency-budget', label: 'Restore latency budget' },
  'dns-failure': { action: 'dns-recovery', label: 'Restore DNS resolution' },
  'retry-storm': { action: 'retry-budget', label: 'Stop retry amplification' },
  'memory-leak': { action: 'replace-capacity', label: 'Replace unhealthy capacity' },
  'autoscaler-oscillation': { action: 'stabilize-policy', label: 'Stabilize autoscaler policy' },
  'blocked-traffic': { action: 'network-policy-recovery', label: 'Correct egress policy' },
};
const diagnosisOptions: Record<string, { value: string; label: string }[]> = {
  'cpu-saturation': [{ value: 'api-capacity', label: 'API capacity / CPU saturation' }, { value: 'dependency', label: 'Dependency failure' }, { value: 'latency', label: 'Latency-only regression' }],
  'useful-alerts': [{ value: 'user-impact', label: 'User-impact signal' }, { value: 'api-capacity', label: 'CPU capacity noise' }, { value: 'dependency', label: 'Dependency failure' }],
  'slo-burn-rate': [{ value: 'error-budget-burn', label: 'Rapid error-budget burn' }, { value: 'user-impact', label: 'Generic user impact' }, { value: 'latency', label: 'Latency SLO breach' }],
  'vertical-horizontal': [{ value: 'api-capacity', label: 'API capacity constraint' }, { value: 'dependency', label: 'Dependency bottleneck' }, { value: 'concurrency', label: 'Concurrency limit' }],
  'dependency-bottleneck': [{ value: 'dependency', label: 'Dependency bottleneck' }, { value: 'api-capacity', label: 'API capacity constraint' }, { value: 'latency', label: 'Client-side latency' }],
  'connection-pool': [{ value: 'concurrency', label: 'Connection pool / concurrency exhaustion' }, { value: 'api-capacity', label: 'CPU capacity' }, { value: 'dependency', label: 'Dependency failure' }],
  'latency-slo': [{ value: 'latency', label: 'Latency SLO violation' }, { value: 'user-impact', label: 'Availability failure' }, { value: 'dependency', label: 'Dependency failure' }],
  'dns-failure': [{ value: 'service-discovery', label: 'Service discovery / DNS failure' }, { value: 'dependency', label: 'Dependency process failure' }, { value: 'api-capacity', label: 'API capacity' }],
  'retry-storm': [{ value: 'retry-amplification', label: 'Retry amplification' }, { value: 'dependency', label: 'Dependency-only failure' }, { value: 'api-capacity', label: 'API capacity' }],
  'memory-leak': [{ value: 'memory-growth', label: 'Unbounded memory growth' }, { value: 'api-capacity', label: 'CPU capacity' }, { value: 'dependency', label: 'Dependency failure' }],
  'autoscaler-oscillation': [{ value: 'feedback-loop', label: 'Unstable scaling feedback loop' }, { value: 'api-capacity', label: 'Static API capacity' }, { value: 'dependency', label: 'Dependency bottleneck' }],
  'file-forensics': [{ value: 'file-owner', label: 'Process owns the open file descriptor' }, { value: 'memory-growth', label: 'Memory growth' }, { value: 'dependency', label: 'Dependency failure' }],
  'blocked-traffic': [{ value: 'network-policy', label: 'Narrow egress policy blocks the flow' }, { value: 'service-discovery', label: 'DNS resolution failure' }, { value: 'dependency', label: 'Dependency process failure' }],
};
type ComponentName = typeof names[number];
type LabStatus = {
  status: 'healthy' | 'degraded';
  components: { name: ComponentName; status: 'healthy' | 'unavailable' }[];
  phase: 1;
};
type RunState = {
  state: 'idle' | 'starting' | 'running' | 'error';
  runID?: string;
  scenario?: string;
  phase?: string;
  revealedHints?: number[];
  alertExpression?: string;
  alertFired?: boolean;
  alertCleared?: boolean;
  diagnosis?: string;
  evidenceNote?: string;
};
type RunHistory = { runID: string; scenario: string; checkedAt: string; passed: boolean; availability?: number; p95LatencyMS?: number };
type ScenarioCatalogItem = { id: string; diagram?: DiagramData; hints?: string[]; goal?: string; successCriteria?: string[] };

function parseStatus(value: unknown): LabStatus {
  const data = value as LabStatus | null;
  if (!data || data.phase !== 1 || !['healthy', 'degraded'].includes(data.status)
    || !Array.isArray(data.components) || data.components.length !== names.length
    || !names.every(name => data.components.filter(component => component?.name === name
      && ['healthy', 'unavailable'].includes(component.status)).length === 1)
    || (data.status === 'healthy' && data.components.some(component => component.status !== 'healthy'))) {
    throw new Error('The server returned an invalid Phase 1 status.');
  }
  return data;
}

export function App() {
  const [status, setStatus] = useState<LabStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [route, setRoute] = useState(window.location.hash);
  const [search, setSearch] = useState('');
  const [difficulty, setDifficulty] = useState('All levels');
  const [pending, setPending] = useState(false);
  const actionLock = useRef(false);
  const [tool, setTool] = useState('metrics');
  const [checked, setChecked] = useState<number[]>([]);
  const [notes, setNotes] = useState('');
  const [progressReady, setProgressReady] = useState(false);
  const [progressState, setProgressState] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle');
  const [run, setRun] = useState<RunState>({ state: 'idle' });
  const [runMessage, setRunMessage] = useState('');
  const [alertExpression, setAlertExpression] = useState('');
  const [diagnosis, setDiagnosis] = useState('');
  const [evidenceNote, setEvidenceNote] = useState('');
  const [grade, setGrade] = useState<{ passed?: boolean; feedback?: string; availability?: number; p95_latency_ms?: number; requests?: number; objective_availability?: number; objective_p95_latency_ms?: number; minimum_requests?: number } | null>(null);
  const [history, setHistory] = useState<RunHistory[]>([]);
  const [scenarioCatalog, setScenarioCatalog] = useState<ScenarioCatalogItem[]>([]);
  const requestedExercise = exercises.find(item => route === `#/exercises/${item.id}`);
  const exercise = requestedExercise;
  const walkthrough = exercise?.id === 'first-investigation';
  const exerciseMode = scenarioExerciseIDs.includes(exercise?.id ?? '');
  const activeRun = exerciseMode && run.scenario === exercise?.id && run.state === 'running';
  const saveVersion = useRef(0);
  const saveQueue = useRef(Promise.resolve());
  const isIndex = !route || route === '#/' || route === '#';
  const filtered = exercises.filter(item => (difficulty === 'All levels' || item.difficulty === difficulty) && `${item.title} ${item.category} ${item.summary}`.toLowerCase().includes(search.toLowerCase().trim()));
  const learningPath = ['first-investigation', 'file-forensics', 'cpu-saturation', 'dependency-bottleneck', 'useful-alerts', 'slo-burn-rate'];
  const nextExercise = exercises.find(item => item.id === learningPath[learningPath.indexOf(exercise?.id ?? '') + 1]);
  const selfCheck = walkthrough || exercise?.id === 'file-forensics';
  const selectedScenario = scenarioCatalog.find(item => item.id === exercise?.id);
  const diagnosisRecorded = activeRun && !!run.diagnosis;

  useEffect(() => { setGrade(null); setRunMessage(''); setTool('metrics'); }, [exercise?.id]);
  const categories = Array.from(new Set(filtered.map(item => item.category)));

  useEffect(() => {
    const navigate = () => { setRoute(window.location.hash); window.scrollTo(0, 0); };
    window.addEventListener('hashchange', navigate);
    return () => window.removeEventListener('hashchange', navigate);
  }, []);

  useEffect(() => {
    if (!walkthrough || !exercise) {
      setChecked([]);
      setNotes('');
      setProgressReady(false);
      setProgressState('idle');
      return;
    }
    let cancelled = false;
    setProgressReady(false);
    setProgressState('idle');
    fetch(`/api/progress/${exercise.id}`, { cache: 'no-store' })
      .then(async response => {
        if (!response.ok) throw new Error(`Progress request failed (HTTP ${response.status}).`);
        const data = await response.json() as { completedSteps?: unknown; notes?: unknown };
        if (!Array.isArray(data.completedSteps) || data.completedSteps.some(step => !Number.isInteger(step) || step < 0 || step >= (exercise.steps?.length ?? 0))) {
          throw new Error('Saved progress is invalid for this walkthrough.');
        }
        if (!cancelled) setChecked(data.completedSteps);
        if (!cancelled && typeof data.notes === 'string') setNotes(data.notes);
      })
      .catch(() => { if (!cancelled) setProgressState('error'); })
      .finally(() => { if (!cancelled) setProgressReady(true); });
    return () => { cancelled = true; };
  }, [exercise?.id, walkthrough]);

  useEffect(() => {
    if (!walkthrough || !exercise || !progressReady) return;
    const version = ++saveVersion.current;
    setProgressState('saving');
    const timer = setTimeout(() => {
      const save = () => fetch(`/api/progress/${exercise.id}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ completedSteps: checked, notes }),
        }).then(response => {
          if (!response.ok) throw new Error(`Progress save failed (HTTP ${response.status}).`);
        });
      // A learner can type or tick quickly. Preserve that event order at the
      // server, rather than allowing an older HTTP request to overwrite a newer one.
      saveQueue.current = saveQueue.current.catch(() => undefined).then(save);
      void saveQueue.current.then(() => {
        if (saveVersion.current === version) setProgressState('saved');
      }).catch(() => {
        if (saveVersion.current === version) setProgressState('error');
      });
    }, 300);
    return () => clearTimeout(timer);
  }, [checked, notes, exercise?.id, walkthrough, progressReady]);

  useEffect(() => {
    if (!exerciseMode || !exercise) {
      setRun({ state: 'idle' });
      setGrade(null);
      return;
    }
    let stopped = false;
    const poll = async () => {
      try {
        const response = await fetch('/api/run', { cache: 'no-store' });
        if (response.ok && !stopped) {
          const data = await response.json() as RunState;
          setRun(data);
          if (data.scenario === exercise.id && data.alertExpression) setAlertExpression(data.alertExpression);
        }
      } catch { /* status remains visible; the next poll retries */ }
    };
    void poll();
    const timer = setInterval(() => void poll(), 3000);
    return () => { stopped = true; clearInterval(timer); };
  }, [exercise?.id, exerciseMode]);

  useEffect(() => {
    fetch('/api/scenarios', { cache: 'no-store' }).then(response => response.ok ? response.json() as Promise<ScenarioCatalogItem[]> : []).then(setScenarioCatalog).catch(() => setScenarioCatalog([]));
  }, []);

  useEffect(() => {
    let stopped = false;
    const poll = async () => {
      try {
        const response = await fetch('/api/runs', { cache: 'no-store' });
        if (response.ok && !stopped) setHistory(await response.json() as RunHistory[]);
      } catch { /* the current run remains usable if history is unavailable */ }
    };
    void poll();
    const timer = setInterval(() => void poll(), 5000);
    return () => { stopped = true; clearInterval(timer); };
  }, [exerciseMode]);

  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    let controller: AbortController;
    async function poll() {
      controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), 8000);
      try {
        const response = await fetch('/api/status', { signal: controller.signal, cache: 'no-store' });
        if (!response.ok) throw new Error(`Status request failed (HTTP ${response.status}).`);
        const data = parseStatus(await response.json());
        if (!stopped) { setStatus(data); setError(null); }
      } catch (cause) {
        if (!stopped) {
          setStatus(null);
          setError(cause instanceof Error && cause.name !== 'AbortError'
            ? cause.message : 'Status request timed out.');
        }
      } finally {
        clearTimeout(timeout);
        if (!stopped) timer = setTimeout(poll, 5000);
      }
    }
    void poll();
    return () => { stopped = true; clearTimeout(timer); controller?.abort(); };
  }, []);

  const label = error ? 'Status unavailable' : !status ? 'Checking system' : status.status === 'healthy' ? 'System healthy' : 'System degraded';
  const componentStatus = (name: ComponentName) => status?.components.find(component => component.name === name)?.status ?? 'unknown';
  const updateChecklist = (next: number[]) => {
    setProgressState('saving');
    setChecked(next);
  };
  const updateNotes = (next: string) => {
    setProgressState('saving');
    setNotes(next);
  };
  const runtimeAction = async (path: string, options?: RequestInit) => {
    if (actionLock.current) return null;
    actionLock.current = true;
    setPending(true);
    setRunMessage('Working...');
    try {
      const response = await fetch(path, { ...options, headers: { 'Content-Type': 'application/json', ...(options?.headers ?? {}) } });
      const text = await response.text();
      let data;
      try { data = JSON.parse(text); } catch { throw new Error(text.trim() || `Request failed (HTTP ${response.status}).`); }
      if (!response.ok) throw new Error(data.error || data.message || `Request failed (HTTP ${response.status}).`);
      if (data.state) setRun(data);
      if (data.revealedHints) setRun(current => ({ ...current, revealedHints: data.revealedHints }));
      setRunMessage('Action completed.');
      return data;
    } catch (cause) {
      setRunMessage(cause instanceof Error ? cause.message : 'Request failed.');
      return null;
    } finally {
      actionLock.current = false;
      setPending(false);
    }
  };
  const startExercise = () => void runtimeAction(`/api/scenarios/${exercise?.id}/start`, { method: 'POST', body: '{}' });
  const resetExercise = () => { setGrade(null); void runtimeAction('/api/run/reset', { method: 'POST', body: '{}' }); };
  const setPhase = (phase: string) => void runtimeAction('/api/run/phase', { method: 'POST', body: JSON.stringify({ phase }) });
  const checkExercise = async () => { const data = await runtimeAction('/api/run/check', { method: 'POST', body: '{}' }); if (data) setGrade(data); };
  const saveAlert = () => void runtimeAction('/api/run/alert', { method: 'PUT', body: JSON.stringify({ expression: alertExpression }) });
  const submitDiagnosis = async () => { const data = await runtimeAction('/api/run/diagnosis', { method: 'POST', body: JSON.stringify({ diagnosis, evidence: evidenceNote }) }); if (data) { setDiagnosis(''); setEvidenceNote(''); } };

  return (
    <main id="main-content" aria-busy={pending}>
      <a className="skip-link" href="#workspace" onClick={event => { event.preventDefault(); const target = document.getElementById(isIndex ? 'catalog-heading' : 'workspace'); target?.focus(); target?.scrollIntoView(); }}>Skip to workspace</a>
      <header className="page-header">
        <div><p className="eyebrow">Learn by investigating</p><h1><a href="#/">SRE Lab<span className="brand-dot" aria-hidden="true">.</span></a></h1></div>
        <a className="external-link" href="/prometheus/" target="_blank" rel="noreferrer">Open Prometheus</a>
      </header>

      {isIndex ? <section className="catalog" aria-labelledby="catalog-heading">
        <section className="welcome"><div><p className="eyebrow">Your next incident starts here</p><h2>Build confidence troubleshooting real systems.</h2><p className="intro">Follow the evidence. Test a hypothesis. Learn what healthy looks like—with real processes, a live terminal, and room to practice.</p><a className="primary-link" href="#/exercises/first-investigation">Start your first investigation →</a><p className="phase-note">Beginner-friendly · Guided commands · No SRE experience required</p></div><aside><span className="eyebrow">Your practice loop</span><ol><li><strong>01 / Observe</strong><p>Understand the symptoms.</p></li><li><strong>02 / Investigate</strong><p>Find evidence in real tools.</p></li><li><strong>03 / Verify</strong><p>Explain what changed and why.</p></li></ol></aside></section>
        <nav className="learning-path" aria-label="Suggested learning path"><h3>A path to build your confidence</h3><div>{learningPath.map((id, index) => <a key={id} href={`#/exercises/${id}`}><span>{String(index + 1).padStart(2, '0')}</span>{exercises.find(item => item.id === id)?.title}</a>)}</div></nav>
        <p className="eyebrow">Observe / Investigate / Verify</p>
        <h2 id="catalog-heading" tabIndex={-1}>Exercise index</h2>
        <p className="intro">Start with the guided walkthrough to learn your way around the lab. Each exercise has its own brief, objectives and investigation workspace.</p>
         <p className="phase-note">One guided walkthrough and {scenarioExerciseIDs.length} hands-on exercises. Read a brief, then start when you’re ready.</p>
        <label className="catalog-search">Find an exercise
          <input type="search" value={search} onChange={event => setSearch(event.target.value)} placeholder="Search by title, topic or skill" />
        </label>
        <label className="level-filter">Experience level <select value={difficulty} onChange={event => setDifficulty(event.target.value)}>{['All levels', 'Beginner', 'Intermediate', 'Advanced'].map(level => <option key={level}>{level}</option>)}</select></label>
        <p className="result-count" aria-live="polite">{filtered.length} {filtered.length === 1 ? 'entry' : 'entries'}</p>
        {categories.map(category => <section className="catalog-category" aria-labelledby={`category-${category}`} key={category}><div className="category-heading"><h3 id={`category-${category}`}>{category}</h3><span>{filtered.filter(item => item.category === category).length} exercises</span></div><div className="exercise-grid">{filtered.filter(item => item.category === category).map(item => <article className={`exercise-card ${item.status}`} key={item.id}>
          <div className="card-meta"><span>{item.difficulty}</span><span className="phase">{item.status === 'available' ? (item.id === 'first-investigation' ? 'Available walkthrough' : 'Available exercise') : 'Planned'}</span></div>
          <h3>{item.title}</h3><p>{item.summary}</p>
          {history.some(record => record.scenario === item.id && record.passed) && <span className="completion-badge">✓ {item.id === 'file-forensics' ? 'Self-check recorded' : 'Solution check passed'} · shared lab history</span>}
          <div className="card-action"><span>{item.duration}</span><a className={item.status === 'available' ? 'primary-link' : 'preview-link'} href={`#/exercises/${item.id}`}>{item.status === 'available' ? (item.id === 'first-investigation' ? 'Start walkthrough' : 'Open exercise') : 'View planned brief'}</a></div>
        </article>)}</div></section>)}
        {filtered.length === 0 && <p className="empty-state">No exercises match this search. Try a topic such as capacity, alerting or reliability.</p>}
      </section> : !exercise ? <section className="catalog"><h2>Exercise not available</h2><p>This link does not point to an available exercise brief.</p><a href="#/">Back to exercise index</a></section> : <>
        <nav className="breadcrumb" aria-label="Breadcrumb"><a href="#/">Exercise index</a><span>/ {walkthrough ? 'Guided walkthrough' : 'Hands-on exercise'}</span></nav>
        <section className="exercise-brief" aria-labelledby="exercise-heading">
          <p className="eyebrow">{exercise.category} / {exercise.difficulty} / {exercise.duration}</p>
          <h2 id="exercise-heading">{exercise.title}</h2>
          <p className="intro">{exercise.situation}</p>
          <div className="objective"><h3>Your objective</h3><p>{exercise.objective}</p></div>
          {!walkthrough && <section className="goal-contract"><p className="eyebrow">Investigation contract</p><h3>{selectedScenario?.goal ?? exercise.objective}</h3><p>Choose the explanation your evidence supports. The lab accepts a mitigation only after you record a diagnosis.</p><ul>{selectedScenario?.successCriteria?.map(criteria => <li key={criteria}>{criteria}</li>)}</ul><label>My diagnosis<select value={diagnosis} onChange={event => setDiagnosis(event.target.value)}><option value="">Select the hypothesis your evidence supports</option>{diagnosisOptions[exercise.id]?.map(option => <option value={option.value} key={option.value}>{option.label}</option>)}</select></label><label>Evidence note<textarea value={evidenceNote} minLength={10} maxLength={2000} onChange={event => setEvidenceNote(event.target.value)} placeholder="What did you observe, and why does it support this diagnosis?" /></label><button className="primary-button" disabled={!activeRun || !diagnosis || evidenceNote.trim().length < 10 || pending} onClick={() => void submitDiagnosis()}>{diagnosisRecorded ? 'Update diagnosis' : 'Record diagnosis'}</button>{diagnosisRecorded && <p className="action-message">Diagnosis recorded. You can now test a mitigation and verify the goal.</p>}</section>}
          <ul className="learning-objectives">{exercise.learningObjectives?.map(item => <li key={item}>{item}</li>)}</ul>
          {walkthrough ? <p className="phase-note">No automatic grade: mark each step after checking its expected result. Your self-check saves automatically in this local lab and is restored when you return. `make reset` clears saved progress with the lab baseline.</p>
            : <div className="runtime-controls"><p className="phase-note"><strong>{selfCheck ? 'Guided self-check.' : 'Executable exercise.'}</strong> {selfCheck ? 'Find the process, PID, file descriptor, and file path. Mark your evidence when you have verified them yourself.' : 'Start the scenario, investigate in the terminal, then check the observed outcome.'}</p><fieldset disabled={pending} className="button-row">{!activeRun ? <button className="primary-button" onClick={startExercise}>Start exercise</button> : <><button className="secondary-button" onClick={resetExercise}>Reset exercise</button><button className="primary-button" onClick={checkExercise}>{selfCheck ? 'Review self-check' : 'Check solution'}</button></>}</fieldset>{runMessage && <p role="status" className="action-message">{runMessage}</p>}{activeRun && <p className="run-status">Run <code>{run.runID}</code> / phase <strong>{run.phase}</strong></p>}</div>}
        </section>
      </>}

      {exercise && <nav className="workspace-jumps" aria-label="Workspace navigation"><button className="secondary-button" onClick={() => document.querySelector('.instructions')?.scrollIntoView()}>Instructions ↓</button><button className="secondary-button" onClick={() => document.querySelector('.lab-workspace')?.scrollIntoView()}>Tools ↓</button><span className="phase-note">Environment: {label}</span></nav>}
      <div id="workspace" tabIndex={-1} className={exercise ? 'exercise-workspace' : ''}>
      {exercise && <aside className="instructions" aria-labelledby="instructions-heading">
        <h2 id="instructions-heading">{walkthrough ? 'What to do' : activeRun ? 'Incident runbook' : 'Exercise runbook'}</h2>
        {walkthrough && <p className="checklist-progress" aria-live="polite">{progressReady ? `${checked.length} of ${exercise.steps?.length} steps checked` : 'Restoring saved progress...'}</p>}
        <ol className="commands">{exercise.steps?.map((step, index) => <li key={step.title}>
          <h3>{step.title}</h3><p>{step.instruction}</p>
          {'command' in step && step.command && <CommandBlock command={step.command} />}
          <p className="expected"><strong>Expected result:</strong> {step.expected}</p>
          <div className="reflection"><p><strong>Why it matters:</strong> {step.question}</p><details><summary>Reveal explanation</summary><p>{step.answer}</p></details></div>
          {walkthrough && <label className="step-check"><input type="checkbox" disabled={!progressReady} checked={checked.includes(index)} onChange={event => updateChecklist(event.target.checked ? [...checked, index] : checked.filter(value => value !== index))} />I checked step {index + 1}'s result</label>}
        </li>)}</ol>
        {walkthrough && checked.length === exercise.steps?.length && <p className="completion" role="status">Walkthrough self-check complete. You have followed a request and found its metrics. This is your checklist, not an automatically verified grade.</p>}
        {walkthrough && <><label className="notes-label" htmlFor={`notes-${exercise.id}`}>Investigation notes<textarea id={`notes-${exercise.id}`} maxLength={16000} disabled={!progressReady} value={notes} onChange={event => updateNotes(event.target.value)} placeholder="Save observations, hypotheses, commands and next steps here." /></label><button className="secondary-button" disabled={!progressReady} onClick={() => updateChecklist([])}>Clear checklist</button><p className={`save-state ${progressState}`}>{progressState === 'saving' ? 'Saving progress...' : progressState === 'saved' ? 'Progress saved locally.' : progressState === 'error' ? 'Progress could not be saved. Your current work is still visible.' : ''}</p><p className="phase-note">If a request fails, read the HTTP status and check the service health on the right. If metrics are empty, make a request and wait for a scrape. Nothing here requires scaling or fixing an injected fault.</p></>}
          {exerciseMode && <><h3 className="hints-heading">Hints</h3><ol className="hint-list">{exercise.hints?.map((hint, index) => <li key={hint}>{run?.revealedHints?.includes(index) ? hint : 'Hint available'}</li>)}</ol>{activeRun && <div className="button-row"><button className="secondary-button" onClick={() => void runtimeAction('/api/run/hint', { method: 'POST', body: '{}' })}>Reveal next hint</button>{exercise.id === 'file-forensics' && <button className="secondary-button" disabled={!diagnosisRecorded || pending} onClick={() => void runtimeAction('/api/run/file-evidence', { method: 'POST', body: '{}' })}>Mark lsof evidence found</button>}{['cpu-saturation', 'vertical-horizontal'].includes(exercise.id) && <button className="secondary-button" disabled={!diagnosisRecorded || pending} onClick={() => void runtimeAction('/api/run/capacity', { method: 'POST', body: JSON.stringify({ activeBackends: 2 }) })}>Apply capacity change</button>}{(exercise.id === 'useful-alerts' || exercise.id === 'slo-burn-rate') && <><button className="secondary-button" onClick={() => setPhase('incident')}>Inject incident phase</button><button className="secondary-button" disabled={!diagnosisRecorded || pending} onClick={() => setPhase('recovery')}>Start recovery phase</button></>}{interventionLabels[exercise.id] && <button className="secondary-button" disabled={!diagnosisRecorded || pending} onClick={() => void runtimeAction('/api/run/intervention', { method: 'POST', body: JSON.stringify({ action: interventionLabels[exercise.id].action }) })}>{interventionLabels[exercise.id].label}</button>}</div>}{(exercise.id === 'useful-alerts' || exercise.id === 'slo-burn-rate') && activeRun && <label className="notes-label">PromQL alert expression<textarea value={alertExpression} onChange={event => setAlertExpression(event.target.value)} placeholder="Enter a read-only PromQL alert expression" /><button className="secondary-button" disabled={!diagnosisRecorded || pending} onClick={saveAlert}>Save alert expression</button></label>}</>}
          {grade && <div className={`grade ${grade.passed ? 'pass' : 'fail'}`} role="status"><strong>{selfCheck ? (grade.passed ? 'Self-check recorded' : 'Record your investigation first') : grade.passed ? 'Solution check passed' : 'Solution check needs more evidence'}</strong><p>{selfCheck ? 'This records your confirmation. The lab does not automatically inspect your lsof evidence.' : grade.feedback}</p>{!selfCheck && grade.availability !== undefined && <small>Availability {((grade.availability ?? 0) * 100).toFixed(2)}% / p95 {Math.round(grade.p95_latency_ms ?? 0)} ms / {Math.round(grade.requests ?? 0)} requests</small>}</div>}
          {grade && !selfCheck && !grade.passed && !['useful-alerts', 'slo-burn-rate'].includes(exercise.id) && <ul className="grade-guidance">{grade.objective_availability !== undefined && (grade.availability ?? 0) < grade.objective_availability && <li>Availability is below {(grade.objective_availability * 100).toFixed(1)}%. Investigate failed requests before checking again.</li>}{grade.objective_p95_latency_ms !== undefined && (grade.p95_latency_ms ?? 0) > grade.objective_p95_latency_ms && <li>Latency is above {grade.objective_p95_latency_ms} ms. Continue investigating the slow path under load.</li>}{grade.minimum_requests !== undefined && (grade.requests ?? 0) < grade.minimum_requests && <li>More samples are needed. Keep traffic running and wait for fresh metrics before checking again.</li>}</ul>}
          {(grade?.passed || (walkthrough && progressReady && checked.length === exercise.steps?.length)) && <section className="completion-panel"><p className="eyebrow">Keep building your skills</p><h3>Take the learning with you.</h3><ul>{exercise.learningObjectives?.map(skill => <li key={skill}>{skill}</li>)}</ul><p>Before moving on, note the evidence that changed your hypothesis.{activeRun && ' Reset this run to leave the lab ready for your next exercise.'}</p>{nextExercise && <a className="primary-link" href={`#/exercises/${nextExercise.id}`}>Next: {nextExercise.title} →</a>}<a href="#/">Explore the exercise library</a></section>}
          {exerciseMode && history.filter(item => item.scenario === exercise.id).length > 0 && <section className="run-history" aria-labelledby="history-heading"><h3 id="history-heading">Recent checks</h3><p className="phase-note">Shared lab history</p><ul>{history.filter(item => item.scenario === exercise.id).slice(-5).reverse().map(item => <li key={`${item.runID}-${item.checkedAt}`}><span>{item.passed ? (selfCheck ? 'Self-check recorded' : 'Passed') : 'Needs evidence'}</span><time dateTime={item.checkedAt}>{new Date(item.checkedAt).toLocaleString()}</time>{!selfCheck && item.p95LatencyMS !== undefined && <small>{Math.round(item.p95LatencyMS)} ms p95</small>}</li>)}</ul></section>}
      </aside>}
       <div className="lab-workspace">
        {exercise && <LabTerminal />}
        {exercise && <><nav className="workspace-tools" aria-label="Investigation tools">{(walkthrough ? ['metrics', 'topology'] : ['metrics', 'topology', 'notes']).map(name => <button key={name} className="secondary-button" aria-pressed={tool === name} onClick={() => setTool(name)}>{name[0].toUpperCase() + name.slice(1)}</button>)}</nav>{!walkthrough && <section hidden={tool !== 'notes'}><InvestigationNotes key={exercise.id} id={exercise.id} /></section>}<section hidden={tool !== 'topology'}><ResourceDiagram id={exercise.id} active={activeRun} phase={run.phase} diagram={scenarioCatalog.find(item => item.id === exercise.id)?.diagram} /></section></>}
        <div hidden={!!exercise && tool !== 'metrics'}>
       <section className="overview" aria-labelledby="system-heading">
        <div className="overview-heading">
          <div><p className="eyebrow">Environment</p><h2 id="system-heading" role="status" className={`system-status ${status?.status ?? 'unknown'}`}><span className="dot" />{label}</h2></div>
          <span className="phase">Exercises / Docker runtime</span>
        </div>
        <p className="intro">{walkthrough || activeRun ? 'Use this live environment to work through the instructions. Connect the terminal below to begin.' : exercise ? 'Read the runbook, then start the deterministic scenario when you are ready.' : 'The shared lab is ready for investigation. Choose an exercise above to open its runbook and terminal.'}</p>
        <p className="phase-note">{selfCheck ? 'Guided self-check: investigate with real tools and record your own evidence.' : 'Solution checks use runtime evidence. Read the feedback to see which objectives still need work.'}</p>
        {error && <p role="alert" className="error">{error} Retrying automatically; component health is unknown.</p>}
        <div className="environment-grid">
          <div className="topology" aria-label="Service topology: API calls dependency">
             <div className="service"><span className="service-name">API pool</span><code>api-lb:8080</code><span className={`component-state ${componentStatus('api')}`}>{componentStatus('api')}</span></div>
            <span className="connection" aria-label="calls">&rarr;<small>HTTP</small></span>
            <div className="service"><span className="service-name">Dependency</span><code>dependency:8080</code><span className={`component-state ${componentStatus('dependency')}`}>{componentStatus('dependency')}</span></div>
          </div>
          <dl className="support-services">
            <div><dt>Prometheus <small>Metrics collection</small></dt><dd className={`component-state ${componentStatus('prometheus')}`}>{componentStatus('prometheus')}</dd></div>
            <div><dt>Toolbox <small>Isolated shell</small></dt><dd className={`component-state ${componentStatus('toolbox')}`}>{componentStatus('toolbox')}</dd></div>
          </dl>
        </div>
       </section>

       {(exerciseMode || walkthrough) && <LiveMetrics runID={run.runID} active={activeRun} />}

       </div>
      </div></div>
      <footer>Built for curious engineers. Real processes and metrics. Guided self-checks and runtime-based solution checks.</footer>
    </main>
  );
}

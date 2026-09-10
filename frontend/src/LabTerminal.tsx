import { useEffect, useRef, useState } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';

type ConnectionState = 'disconnected' | 'connecting' | 'connected' | 'error';

export function LabTerminal() {
  const host = useRef<HTMLDivElement>(null);
  const connect = useRef<() => void>(() => {});
  const disconnect = useRef<() => void>(() => {});
  const [state, setState] = useState<ConnectionState>('disconnected');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const terminal = new Terminal({
      cursorBlink: true, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      fontSize: 14, lineHeight: 1.25, scrollback: 2000, screenReaderMode: true,
      theme: { background: '#172321', foreground: '#e5ede7', cursor: '#b5d9bf', selectionBackground: '#496156' },
    });
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.open(host.current!);
    terminal.textarea?.setAttribute('aria-label', 'Toolbox terminal input');
    let socket: WebSocket | null = null;
    let connectionTimeout: ReturnType<typeof setTimeout> | undefined;
    let frame = 0;
    let disposed = false;

    function size() {
      if (disposed || !host.current?.clientWidth || !host.current.clientHeight) return;
      fit.fit();
      if (socket?.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: 'resize', cols: terminal.cols, rows: terminal.rows }));
      }
    }

    function close() {
      clearTimeout(connectionTimeout);
      if (socket) {
        socket.onopen = socket.onmessage = socket.onerror = socket.onclose = null;
        socket.close();
        socket = null;
      }
    }

    connect.current = () => {
      if (socket) return;
      setError(null);
      setState('connecting');
      terminal.reset();
      try {
        const url = new URL('/terminal', window.location.href);
        url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
        const current = new WebSocket(url);
        socket = current;
        current.binaryType = 'arraybuffer';
        connectionTimeout = setTimeout(() => {
          close();
          setState('error');
          setError('Connection timed out. Check toolbox health and try connecting again.');
        }, 10000);
        current.onopen = () => {
          clearTimeout(connectionTimeout);
          setState('connected');
          size();
          terminal.focus();
        };
        current.onmessage = event => {
          if (event.data instanceof ArrayBuffer) terminal.write(new Uint8Array(event.data));
          else {
            close();
            setState('error');
            setError('Unexpected terminal response: expected binary PTY output.');
          }
        };
        current.onerror = () => {
          close();
          setState('error');
          setError('Could not connect to the toolbox. Check system health and the server origin configuration.');
        };
        current.onclose = event => {
          clearTimeout(connectionTimeout);
          socket = null;
          setState('error');
          setError(`Terminal session ended (code ${event.code}). Connect again to start a new shell.`);
        };
      } catch {
        close();
        setState('error');
        setError('The browser could not open a terminal connection. Try connecting again.');
      }
    };
    disconnect.current = () => {
      close();
      setState('disconnected');
      setError(null);
    };
    const input = terminal.onData(data => {
      if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: 'input', data }));
    });
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(size);
    });
    observer.observe(host.current!);
    size();
    return () => {
      disposed = true;
      close();
      cancelAnimationFrame(frame);
      observer.disconnect();
      input.dispose();
      terminal.dispose();
      connect.current = disconnect.current = () => {};
    };
  }, []);

  return <section className="terminal-section" aria-labelledby="terminal-heading">
    <div className="terminal-header">
      <div><h2 id="terminal-heading">Toolbox terminal</h2><p>Commands run in an isolated lab container, never on your host.</p></div>
      <div className="terminal-controls"><span role="status" data-testid="terminal-status">{state === 'error' ? 'Connection error' : state[0].toUpperCase() + state.slice(1)}</span>
        {state === 'connected' || state === 'connecting'
          ? <button onClick={() => disconnect.current()}>{state === 'connecting' ? 'Cancel connection' : 'Disconnect'}</button>
          : <button onClick={() => connect.current()}>Connect terminal</button>}
      </div>
    </div>
    {error && <p className="terminal-error" role="alert">{error}</p>}
    <div className="terminal-body">
      <div ref={host} className="terminal-host" data-testid="terminal" />
      {state !== 'connected' && <div className="terminal-overlay"><p>{state === 'connecting' ? 'Opening a toolbox session...' : 'Connect to start a toolbox shell.'}</p><small>Disconnecting ends this session. Reconnecting starts a new shell.</small></div>}
    </div>
    <div className="terminal-footnote">Toolbox has no host filesystem or Docker socket. Use API metrics, not toolbox processes, to inspect the service.</div>
  </section>;
}

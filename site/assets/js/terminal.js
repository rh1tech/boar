/* The browser gets a BBS session, never an operating-system shell. */
(() => {
  'use strict';
  const connect = document.getElementById('terminal-connect');
  const disconnect = document.getElementById('terminal-disconnect');
  const status = document.getElementById('terminal-status');
  const panel = document.getElementById('terminal-panel');
  let terminal;
  let socket;
  let failed = false;
  const encoder = new TextEncoder();

  connect.addEventListener('click', async () => {
    connect.disabled = true;
    failed = false;
    status.textContent = status.dataset.connecting;
    try {
      await document.fonts.ready;
      if (terminal) terminal.dispose();
      panel.hidden = false;
      terminal = new Terminal({
        cols: 80, rows: 25, scrollback: 2000, screenReaderMode: true,
        fontFamily: '"Plex Mono", monospace', fontSize: 14,
        theme: { background: '#000000', foreground: '#aaaaaa', cursor: '#55ffff' },
      });
      terminal.open(document.getElementById('terminal'));
      socket = new WebSocket(`wss://${location.host}/terminal/ws`);
      socket.binaryType = 'arraybuffer';
      socket.onopen = () => {
        status.textContent = status.dataset.connected;
        disconnect.disabled = false;
        terminal.focus();
      };
      socket.onmessage = (event) => {
        if (event.data instanceof ArrayBuffer) terminal.write(new Uint8Array(event.data));
      };
      socket.onerror = () => {
        failed = true;
        status.textContent = status.dataset.error;
      };
      socket.onclose = () => {
        connect.disabled = false;
        disconnect.disabled = true;
        status.textContent = failed ? status.dataset.error : status.dataset.closed;
      };
      terminal.onData((data) => {
        if (socket.readyState !== WebSocket.OPEN) return;
        if (socket.bufferedAmount > 64 * 1024) {
          failed = true;
          status.textContent = status.dataset.error;
          socket.close(1000, 'Input buffer full');
          return;
        }
        const bytes = encoder.encode(data);
        for (let i = 0; i < bytes.length; i += 4096) socket.send(bytes.slice(i, i + 4096));
      });
    } catch (error) {
      console.error('Boar terminal connection failed', error);
      status.textContent = status.dataset.error;
      connect.disabled = false;
      disconnect.disabled = true;
      if (socket) socket.close();
    }
  });
  disconnect.addEventListener('click', () => {
    if (socket) socket.close(1000, 'Caller disconnected');
  });
  window.addEventListener('pagehide', () => {
    if (socket) socket.close(1000, 'Page closed');
  });
})();

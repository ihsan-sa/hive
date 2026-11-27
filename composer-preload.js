// The composer bar's bridge (signed from the start, step 1): only the app's own composer.html gets it. The Hive's page has
// no preload at all, so it can't reach the bar, and main.js checks the sender again before it signs anything.
const { contextBridge, ipcRenderer } = require('electron');
if (location.protocol === 'file:') contextBridge.exposeInMainWorld('bar', {
  send: (text, thread) => ipcRenderer.invoke('bar-send', String(text), String(thread || '')),
  onThread: fn => ipcRenderer.on('thread', (_e, t) => fn(String(t || ''))),
});

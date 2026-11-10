// Only the setup screen gets this bridge: the web app's pages get nothing, and main.js checks the sender again before acting.
const { contextBridge, ipcRenderer } = require('electron');
if (location.protocol === 'file:') contextBridge.exposeInMainWorld('hive', {
  install: accept => ipcRenderer.send('install', !!accept),
  retry: () => ipcRenderer.send('retry'),
  quit: () => ipcRenderer.send('quit'),
  on: (name, fn) => ipcRenderer.on(name, (_e, ...a) => fn(...a)),
});

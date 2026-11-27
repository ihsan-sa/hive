// Only the setup screen gets this bridge: the web app's pages get nothing, and main.js checks the sender again before acting.
const { contextBridge, ipcRenderer } = require('electron');
if (location.protocol === 'file:') contextBridge.exposeInMainWorld('hive', {
  install: accept => ipcRenderer.send('install', !!accept),
  retry: () => ipcRenderer.send('retry'),
  copy: text => ipcRenderer.send('copy', String(text)),
  version: ipcRenderer.sendSync('version'),
  quit: () => ipcRenderer.send('quit'),
  download: () => ipcRenderer.send('download'),
  rosetta: ipcRenderer.sendSync('rosetta'),
  chipDownload: () => ipcRenderer.send('chip-download'),
  askNewer: () => ipcRenderer.send('newer?'),
  // Many Hives: connect by address, open or remove one by its id, and the app's own screens for them.
  connect: address => ipcRenderer.send('connect', String(address)),
  openHive: id => ipcRenderer.send('open-hive', String(id)),
  removeHive: id => ipcRenderer.send('remove-hive', String(id)),
  browserHive: () => ipcRenderer.send('browser-hive'),
  hives: () => ipcRenderer.send('hives'),
  connectScreen: () => ipcRenderer.send('connect-screen'),
  back: () => ipcRenderer.send('back'),
  retryHive: () => ipcRenderer.send('retry-hive'),
  // The rail (step 4): show a Hive in this window, its tile's menu, and a new window.
  railPick: id => ipcRenderer.send('rail-pick', String(id)),
  tileMenu: id => ipcRenderer.send('tile-menu', String(id)),
  newWindow: () => ipcRenderer.send('new-window'),
  on: (name, fn) => ipcRenderer.on(name, (_e, ...a) => fn(...a)),
});

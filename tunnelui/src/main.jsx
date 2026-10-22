import React, { useEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import './style.css'

function Collector() {
  const [files, setFiles] = useState([])
  const [status, setStatus] = useState('')
  const [uploads, setUploads] = useState([])
  const uploadID = useRef(0)
  const refresh = async () => { const r = await fetch('/api/files'); if (r.ok) setFiles((await r.json()).files || []) }
  useEffect(() => { refresh() }, [])
  async function upload(e) {
    const selected = [...e.target.files]; e.target.value = ''
    if (!selected.length) return
    setStatus('')
    for (const file of selected) {
      const id = ++uploadID.current
      setUploads(current => [...current, { id, name: file.name, loaded: 0, total: file.size, state: 'uploading' }])
      const result = await new Promise(resolve => {
        const body = new FormData(); body.append('file', file)
        const request = new XMLHttpRequest()
        request.open('POST', '/api/upload')
        request.upload.onprogress = event => {
          if (event.lengthComputable) setUploads(current => current.map(item => item.id === id ? { ...item, loaded: event.loaded, total: event.total } : item))
        }
        request.onload = () => resolve({ ok: request.status >= 200 && request.status < 300, message: request.status === 409 ? 'A file with that name already exists' : `Upload failed (${request.status})` })
        request.onerror = () => resolve({ ok: false, message: 'Network error during upload' })
        request.onabort = () => resolve({ ok: false, message: 'Upload cancelled' })
        request.send(body)
      })
      setUploads(current => current.map(item => item.id === id ? { ...item, loaded: result.ok ? item.total || 1 : item.loaded, state: result.ok ? 'done' : 'error', message: result.message } : item))
      setStatus(result.ok ? `${file.name} uploaded` : `${file.name}: ${result.message}`)
      if (result.ok) { try { await refresh() } catch { setStatus(`${file.name} uploaded; could not refresh file list`) } }
    }
  }
  return <main className="collector"><header><div className="brand"><span className="mark">S</span><div><b>File collector</b><small>simpleNg</small></div></div><span className="state"><i/> Available</span></header><section><div className="section-head"><div><h1>Incoming files</h1><p>Uploads are stored in the configured directory.</p></div><label className="upload">＋ Upload files<input type="file" multiple onChange={upload}/></label></div>{status && <p className="status" role="status">{status}</p>}{uploads.length > 0 && <div className="uploads" aria-live="polite">{uploads.map((item, index) => <div className="upload-row" key={`${item.name}-${index}`}><div className="upload-meta"><span title={item.name}>{item.name}</span><span>{item.state === 'uploading' ? `${item.total ? Math.floor(item.loaded / item.total * 100) : 0}%` : item.state === 'done' ? 'Complete' : item.message}</span></div><progress value={item.loaded} max={item.total || 1} aria-label={`Uploading ${item.name}`} className={item.state}/></div>)}</div>}<div className="list-head"><span>FILE NAME</span><span>ACCESS</span></div>{files.length ? files.map(f=><div className="file-row" key={f}><span className="file-icon">▤</span><a href={`/api/files/${encodeURIComponent(f)}`} download>{f}</a><span className="locked">Attachment</span></div>) : <div className="empty">No files received</div>}</section></main>
}

function WebSSH() {
  const termRef = useRef(null)
  const [connection, setConnection] = useState(0)
  const [active, setActive] = useState(false)
  useEffect(() => {
    const term = new Terminal({ cursorBlink: true, fontFamily: 'ui-monospace, SFMono-Regular, Consolas, monospace', fontSize: 14, theme: { background: '#111820', foreground: '#dce5e8', cursor: '#4dc6a4' } })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(termRef.current)
    fit.fit()
    const socket = new WebSocket(`${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/ws/ssh`)
    socket.binaryType = 'arraybuffer'
    let message = ''
    socket.onopen = () => { setActive(true); socket.send(JSON.stringify({ cols: term.cols, rows: term.rows })); term.focus() }
    socket.onmessage = event => {
      if (typeof event.data === 'string') {
        try { message = JSON.parse(event.data).message || event.data } catch { message = event.data }
        term.write(`\r\n${message}\r\n`)
      } else term.write(new Uint8Array(event.data))
    }
    socket.onerror = () => { message = 'SSH connection failed'; term.write(`\r\n${message}\r\n`) }
    socket.onclose = () => { setActive(false); if (!message) term.write('\r\nSSH session closed\r\n') }
    const data = term.onData(value => { if (socket.readyState === WebSocket.OPEN) socket.send(new TextEncoder().encode(value)) })
    const binary = term.onBinary(value => { if (socket.readyState === WebSocket.OPEN) socket.send(Uint8Array.from(value, c => c.charCodeAt(0))) })
    const resize = term.onResize(({ cols, rows }) => { if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: 'resize', cols, rows })) })
    const observer = new ResizeObserver(() => fit.fit())
    observer.observe(termRef.current)
    return () => { observer.disconnect(); socket.onclose = null; socket.close(); data.dispose(); binary.dispose(); resize.dispose(); term.dispose() }
  }, [connection])
  return <main className="ssh-page"><header><div className="brand"><span className="mark">S</span><div><b>WebSSH</b><small>simpleNg · Secure shell</small></div></div><span className="state"><i/>{active ? 'Connected' : 'Disconnected'}</span></header><section className="terminal"><div className="terminal-bar"><span>Terminal</span><button onClick={() => setConnection(value => value + 1)}>Reconnect</button></div><div ref={termRef} className="term"/></section></main>
}
function App() {
  const [collector, setCollector] = useState(false)
  useEffect(() => { fetch('/api/files').then(r=>{ if(r.ok) setCollector(true) }).catch(()=>{}) }, [])
  return collector ? <Collector/> : <WebSSH/>
}

createRoot(document.getElementById('root')).render(<App />)

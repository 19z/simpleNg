import React, { useEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import './style.css'

function Collector() {
  const [files, setFiles] = useState([])
  const [status, setStatus] = useState('')
  const refresh = async () => { const r = await fetch('/api/files'); if (r.ok) setFiles((await r.json()).files || []) }
  useEffect(() => { refresh() }, [])
  async function upload(e) {
    const selected = [...e.target.files]; e.target.value = ''
    for (const file of selected) {
      const body = new FormData(); body.append('file', file)
      const response = await fetch('/api/upload', { method: 'POST', body })
      setStatus(response.ok ? `${file.name} uploaded` : `Could not upload ${file.name}`)
      if (response.ok) await refresh()
    }
  }
  return <main className="collector"><header><div className="brand"><span className="mark">S</span><div><b>File collector</b><small>simpleNg</small></div></div><span className="state"><i/> Available</span></header><section><div className="section-head"><div><h1>Incoming files</h1><p>Uploads are stored in the configured directory.</p></div><label className="upload">＋ Upload files<input type="file" multiple onChange={upload}/></label></div>{status && <p className="status">{status}</p>}<div className="list-head"><span>FILE NAME</span><span>ACCESS</span></div>{files.length ? files.map(f=><div className="file-row" key={f}><span className="file-icon">▤</span><a href={`/api/files/${encodeURIComponent(f)}`} download>{f}</a><span className="locked">Attachment</span></div>) : <div className="empty">No files received</div>}</section></main>
}

function WebSSH() {
  const [credentials, setCredentials] = useState({ username: '', password: '' })
  const [error, setError] = useState('')
  const [active, setActive] = useState(false)
  const termRef = useRef(null)
  const socketRef = useRef(null)
  const fitRef = useRef(null)
  const start = e => {
    e.preventDefault(); setError('')
    const term = new Terminal({ cursorBlink: true, fontFamily: 'ui-monospace, SFMono-Regular, Consolas, monospace', fontSize: 14, theme: { background: '#111820', foreground: '#dce5e8', cursor: '#4dc6a4' } })
    const fit = new FitAddon(); term.loadAddon(fit); term.open(termRef.current); fit.fit();
    const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const socket = new WebSocket(`${scheme}//${location.host}/ws/ssh`); socket.binaryType = 'arraybuffer'; socketRef.current = socket; fitRef.current = fit; setActive(true)
    socket.onopen = () => socket.send(JSON.stringify({ ...credentials, cols: term.cols, rows: term.rows }))
    socket.onmessage = event => { if (typeof event.data === 'string') { try { setError(JSON.parse(event.data).message || 'Connection failed') } catch { setError(event.data) } socket.close() } else term.write(new Uint8Array(event.data)) }
    socket.onerror = () => setError('SSH connection failed')
    socket.onclose = () => { setActive(false); setError('SSH session closed') }
    term.onData(data => { if (socket.readyState === WebSocket.OPEN) socket.send(new TextEncoder().encode(data)) })
    term.onResize(({cols, rows}) => { if(socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({type:'resize', cols, rows})) })
    const onResize = () => { fit.fit(); if(socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({type:'resize',cols:term.cols,rows:term.rows})) }
    window.addEventListener('resize', onResize)
    socket.addEventListener('close', () => window.removeEventListener('resize', onResize), { once: true })
  }
  return <main className="ssh-page"><header><div className="brand"><span className="mark">S</span><div><b>WebSSH</b><small>simpleNg · Secure shell</small></div></div><span className="state"><i/> SSH</span></header>{!active ? <form className="login" onSubmit={start}><h1>Connect to host</h1><p>Sign in with your SSH account.</p><label>Username<input required autoComplete="username" value={credentials.username} onChange={e=>setCredentials({...credentials,username:e.target.value})}/></label><label>Password<input required type="password" autoComplete="current-password" value={credentials.password} onChange={e=>setCredentials({...credentials,password:e.target.value})}/></label>{error && <p className="error">{error}</p>}<button>Connect <span>→</span></button></form> : <section className="terminal"><div className="terminal-bar"><span><i/> Connected as {credentials.username}</span><button onClick={()=>{socketRef.current?.close();socketRef.current=null;setActive(false)}}>Disconnect</button></div><div ref={termRef} className="term"/></section>}</main>
}

function App() {
  const [collector, setCollector] = useState(false)
  useEffect(() => { fetch('/api/files').then(r=>{ if(r.ok) setCollector(true) }).catch(()=>{}) }, [])
  return collector ? <Collector/> : <WebSSH/>
}

createRoot(document.getElementById('root')).render(<App />)

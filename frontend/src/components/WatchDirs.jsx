import { useState, useEffect, useCallback } from 'react'
import axios from 'axios'
import { Modal, Button, Alert, Spinner, Card, Row, Col, Form, InputGroup, Pagination } from 'react-bootstrap'

const API_BASE = import.meta.env.VITE_API_URL || 'http://localhost:8085/api'

function formatBytes(bytes) {
  if (!bytes || bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

function formatDate(dateStr) {
  return new Date(dateStr).toLocaleDateString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric'
  })
}

// ── Summary view ─────────────────────────────────────────────────────────────

function SummaryView({ onClose, onBrowse, onGDrive }) {
  const [summaries, setSummaries] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)

  useEffect(() => {
    axios.get(`${API_BASE}/watch-dirs`, { withCredentials: true })
      .then(res => setSummaries(res.data.watch_dirs || []))
      .catch(err => setError(err.response?.data?.error || 'Failed to load watch directories'))
      .finally(() => setLoading(false))
  }, [])

  return (
    <>
      <Modal.Header closeButton>
        <Modal.Title>Import Backup Files</Modal.Title>
      </Modal.Header>
      <Modal.Body>
        {loading && <div className="text-center py-4"><Spinner animation="border" size="sm" className="me-2" />Scanning...</div>}
        {!loading && error && <Alert variant="danger">{error}</Alert>}
        {!loading && !error && summaries.length === 0 && (
          <Alert variant="info">No watch directories configured. Set <code>WATCH_DIRS</code> env var.</Alert>
        )}
        {!loading && !error && summaries.length > 0 && (
          <Row className="g-3">
            {summaries.map(s => (
              <Col key={s.dir} sm={6}>
                <Card className="h-100">
                  <Card.Body>
                    <Card.Subtitle className="text-muted small mb-2"><code>{s.dir}</code></Card.Subtitle>
                    <div className="fw-semibold fs-5">{(s.total_files || 0).toLocaleString()} files</div>
                    <div className="text-muted small mb-3">{formatBytes(s.total_size)}</div>
                    {s.total_files > 0
                      ? <Button size="sm" onClick={() => onBrowse(s.dir)}>Browse &amp; Import</Button>
                      : <Button size="sm" variant="secondary" disabled>No XML files found</Button>
                    }
                  </Card.Body>
                </Card>
              </Col>
            ))}
          </Row>
        )}
      </Modal.Body>
      <Modal.Footer>
        <Button variant="outline-primary" size="sm" onClick={onGDrive}>Google Drive</Button>
        <Button variant="secondary" onClick={onClose}>Close</Button>
      </Modal.Footer>
    </>
  )
}

// ── Per-file browse view ──────────────────────────────────────────────────────

function BrowseView({ dir, onBack }) {
  const [files, setFiles] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [page, setPage] = useState(1)
  const [totalPages, setTotalPages] = useState(1)
  const [total, setTotal] = useState(0)
  const [search, setSearch] = useState('')
  const [searchInput, setSearchInput] = useState('')
  const [sortBy, setSortBy] = useState('date')
  const [sortDir, setSortDir] = useState('desc')
  const [importing, setImporting] = useState({})
  const [results, setResults] = useState({})
  const [selected, setSelected] = useState(new Set())
  const [bulkImporting, setBulkImporting] = useState(false)
  const [bulkResult, setBulkResult] = useState(null)
  const PER_PAGE = 25

  const load = useCallback(async (p, s, sb, sd) => {
    setLoading(true)
    setError(null)
    setSelected(new Set())
    try {
      const res = await axios.get(`${API_BASE}/watch-dirs/browse`, {
        params: { dir, page: p, per_page: PER_PAGE, search: s, sort: sb, sort_dir: sd },
        withCredentials: true
      })
      setFiles(res.data.files || [])
      setPage(res.data.page)
      setTotalPages(res.data.total_pages)
      setTotal(res.data.total)
    } catch (err) {
      setError(err.response?.data?.error || 'Failed to load files')
    } finally {
      setLoading(false)
    }
  }, [dir])

  useEffect(() => { load(1, '', 'date', 'desc') }, [load])

  const handleSearch = (e) => {
    e.preventDefault()
    setSearch(searchInput)
    load(1, searchInput, sortBy, sortDir)
  }

  const handlePageChange = (p) => {
    load(p, search, sortBy, sortDir)
    setResults({})
  }

  const handleSort = (col) => {
    let newDir
    if (col === sortBy) {
      newDir = sortDir === 'asc' ? 'desc' : 'asc'
    } else {
      newDir = col === 'name' ? 'asc' : 'desc'
    }
    setSortBy(col)
    setSortDir(newDir)
    setPage(1)
    load(1, search, col, newDir)
  }

  const SortArrow = ({ col }) => {
    if (sortBy !== col) return <span className="text-muted ms-1" style={{fontSize:'0.7em'}}>↕</span>
    return <span className="ms-1" style={{fontSize:'0.8em'}}>{sortDir === 'asc' ? '↑' : '↓'}</span>
  }

  // ── Selection helpers ──────────────────────────────────────────────────────
  const importableFiles = files.filter(f => !f.queued)
  const allImportableSelected = importableFiles.length > 0 && importableFiles.every(f => selected.has(f.path))
  const someSelected = selected.size > 0

  const toggleSelect = (path) => {
    setSelected(prev => {
      const next = new Set(prev)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })
  }

  const toggleAll = () => {
    if (allImportableSelected) {
      setSelected(prev => {
        const next = new Set(prev)
        importableFiles.forEach(f => next.delete(f.path))
        return next
      })
    } else {
      setSelected(prev => {
        const next = new Set(prev)
        importableFiles.forEach(f => next.add(f.path))
        return next
      })
    }
  }

  // ── Single-file import ──────────────────────────────────────────────────────
  const handleImport = async (file) => {
    setImporting(prev => ({ ...prev, [file.path]: true }))
    try {
      const res = await axios.post(
        `${API_BASE}/watch-dirs/import`,
        { path: file.path },
        { withCredentials: true }
      )
      setResults(prev => ({ ...prev, [file.path]: { success: true, message: res.data.message } }))
    } catch (err) {
      setResults(prev => ({
        ...prev,
        [file.path]: { success: false, message: err.response?.data?.error || 'Import failed' }
      }))
    } finally {
      setImporting(prev => ({ ...prev, [file.path]: false }))
    }
  }

  // ── Bulk import ─────────────────────────────────────────────────────────────
  const handleBulkImport = async () => {
    setBulkImporting(true)
    setBulkResult(null)
    const paths = [...selected]
    try {
      const res = await axios.post(
        `${API_BASE}/watch-dirs/import-batch`,
        { paths },
        { withCredentials: true }
      )
      setBulkResult({ success: true, message: res.data.message })
      setSelected(new Set())
      paths.forEach(path => {
        setResults(prev => ({ ...prev, [path]: { success: true, message: '✓ Queued' } }))
      })
    } catch (err) {
      setBulkResult({ success: false, message: err.response?.data?.error || 'Bulk import failed' })
    } finally {
      setBulkImporting(false)
    }
  }

  // Build compact pagination (max 7 items)
  const buildPages = () => {
    if (totalPages <= 7) return Array.from({ length: totalPages }, (_, i) => i + 1)
    const pages = new Set([1, totalPages, page])
    for (let d = -2; d <= 2; d++) {
      const p = page + d
      if (p > 0 && p <= totalPages) pages.add(p)
    }
    return [...pages].sort((a, b) => a - b)
  }
  const pageNums = buildPages()

  return (
    <>
      <Modal.Header closeButton>
        <Modal.Title>
          <Button variant="link" className="p-0 me-2" onClick={onBack} title="Back">‹</Button>
          <span className="small text-muted fw-normal"><code>{dir}</code></span>
        </Modal.Title>
      </Modal.Header>
      <Modal.Body style={{ maxHeight: '55vh', overflowY: 'auto' }}>
        <Form onSubmit={handleSearch} className="mb-3">
          <InputGroup size="sm">
            <Form.Control
              placeholder="Filter by filename (e.g. sms-2024)"
              value={searchInput}
              onChange={e => setSearchInput(e.target.value)}
            />
            <Button type="submit" variant="outline-secondary">Filter</Button>
            {search && (
              <Button variant="outline-danger" onClick={() => { setSearchInput(''); setSearch(''); load(1, '', sortBy, sortDir) }}>✕</Button>
            )}
          </InputGroup>
        </Form>

        {bulkResult && (
          <Alert variant={bulkResult.success ? 'success' : 'danger'} dismissible onClose={() => setBulkResult(null)}>
            {bulkResult.message}
          </Alert>
        )}

        {loading && <div className="text-center py-3"><Spinner animation="border" size="sm" className="me-2" />Loading...</div>}
        {!loading && error && <Alert variant="danger">{error}</Alert>}
        {!loading && !error && files.length === 0 && (
          <Alert variant="warning">No files found{search ? ` matching "${search}"` : ''}.</Alert>
        )}

        {!loading && !error && files.length > 0 && (
          <table className="table table-sm table-hover align-middle mb-0">
            <thead className="table-light sticky-top">
              <tr>
                <th style={{width: 32}}>
                  <Form.Check
                    type="checkbox"
                    checked={allImportableSelected}
                    onChange={toggleAll}
                    title="Select all importable files on this page"
                    disabled={importableFiles.length === 0}
                  />
                </th>
                <th style={{cursor:'pointer'}} onClick={() => handleSort('name')}>Filename <SortArrow col="name" /></th>
                <th className="text-end" style={{cursor:'pointer'}} onClick={() => handleSort('size')}>Size <SortArrow col="size" /></th>
                <th style={{cursor:'pointer'}} onClick={() => handleSort('date')}>Date <SortArrow col="date" /></th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {files.map(file => {
                const r = results[file.path]
                const busy = importing[file.path]
                const isQueued = file.queued || r?.success
                const isImported = file.imported
                return (
                  <tr key={file.path} className={isQueued ? 'table-warning' : isImported ? 'table-success' : ''}>
                    <td>
                      <Form.Check
                        type="checkbox"
                        checked={selected.has(file.path)}
                        onChange={() => toggleSelect(file.path)}
                        disabled={isQueued}
                      />
                    </td>
                    <td className="text-truncate" style={{ maxWidth: 200 }} title={file.name}>
                      {file.name}
                      {isQueued && <span className="badge bg-warning text-dark ms-1 small">⏳ Queued</span>}
                      {!isQueued && isImported && <span className="badge bg-success ms-1 small">✓ Imported</span>}
                      {r && !r.success && <div className="small text-danger">{r.message}</div>}
                    </td>
                    <td className="text-end text-nowrap text-muted small">{formatBytes(file.size)}</td>
                    <td className="text-nowrap text-muted small">{formatDate(file.modTime)}</td>
                    <td className="text-end">
                      <Button
                        size="sm"
                        variant={isQueued ? 'success' : isImported ? 'outline-secondary' : 'primary'}
                        disabled={busy || isQueued}
                        onClick={() => handleImport(file)}
                      >
                        {busy ? <Spinner animation="border" size="sm" /> : isQueued ? '✓ Queued' : isImported ? 'Re-import' : 'Import'}
                      </Button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </Modal.Body>
      <Modal.Footer className="justify-content-between">
        <span className="text-muted small">
          {total > 0 && `${total.toLocaleString()} file${total !== 1 ? 's' : ''}${search ? ` matching "${search}"` : ''} — sorted by ${sortBy} ${sortDir === 'asc' ? '↑' : '↓'}`}
        </span>
        <div className="d-flex align-items-center gap-2">
          {someSelected && (
            <Button
              size="sm"
              variant="primary"
              disabled={bulkImporting}
              onClick={handleBulkImport}
            >
              {bulkImporting
                ? <><Spinner animation="border" size="sm" className="me-1" />Importing...</>
                : `Import Selected (${selected.size})`
              }
            </Button>
          )}
          {totalPages > 1 && (
            <Pagination size="sm" className="mb-0">
              <Pagination.Prev disabled={page === 1} onClick={() => handlePageChange(page - 1)} />
              {pageNums.map((p, i) => {
                const prev = pageNums[i - 1]
                return (
                  <>
                    {prev && p - prev > 1 && <Pagination.Ellipsis key={`e${p}`} disabled />}
                    <Pagination.Item key={p} active={p === page} onClick={() => p !== page && handlePageChange(p)}>{p}</Pagination.Item>
                  </>
                )
              })}
              <Pagination.Next disabled={page === totalPages} onClick={() => handlePageChange(page + 1)} />
            </Pagination>
          )}
          <Button variant="secondary" size="sm" onClick={onBack}>Back</Button>
        </div>
      </Modal.Footer>
    </>
  )
}

// ── Google Drive view ─────────────────────────────────────────────────────────

function GoogleDriveView({ onBack }) {
  const [status, setStatus] = useState(null)  // {configured, connected}
  const [folders, setFolders] = useState([])
  const [files, setFiles] = useState([])
  const [loading, setLoading] = useState(true)
  const [filesLoading, setFilesLoading] = useState(false)
  const [error, setError] = useState(null)
  const [search, setSearch] = useState('')
  const [searchInput, setSearchInput] = useState('')
  const [selected, setSelected] = useState(new Set())
  const [importing, setImporting] = useState(false)
  const [importResults, setImportResults] = useState([])
  // folderStack: [{id, name}, ...] — last entry is current folder
  const [folderStack, setFolderStack] = useState([{ id: 'root', name: 'My Drive' }])

  useEffect(() => {
    axios.get(`${API_BASE}/gdrive/status`, { withCredentials: true })
      .then(res => {
        setStatus(res.data)
        if (res.data.connected) loadFolder('root')
      })
      .catch(err => setError(err.response?.data?.error || 'Failed to get Drive status'))
      .finally(() => setLoading(false))
  }, [])

  function loadFolder(folderId) {
    setFilesLoading(true)
    setError(null)
    setSelected(new Set())
    axios.get(`${API_BASE}/gdrive/files`, { params: { folder_id: folderId }, withCredentials: true })
      .then(res => {
        setFolders(res.data.folders || [])
        setFiles(res.data.files || [])
      })
      .catch(err => setError(err.response?.data?.error || 'Failed to list Drive files'))
      .finally(() => setFilesLoading(false))
  }

  function loadSearch(q) {
    setFilesLoading(true)
    setError(null)
    setSelected(new Set())
    axios.get(`${API_BASE}/gdrive/files`, { params: { q }, withCredentials: true })
      .then(res => {
        setFolders(res.data.folders || [])
        setFiles(res.data.files || [])
      })
      .catch(err => setError(err.response?.data?.error || 'Failed to search Drive'))
      .finally(() => setFilesLoading(false))
  }

  function handleSearch(e) {
    e.preventDefault()
    const q = searchInput.trim()
    setSearch(q)
    if (q) {
      loadSearch(q)
    } else {
      loadFolder(folderStack[folderStack.length - 1].id)
    }
  }

  function clearSearch() {
    setSearchInput('')
    setSearch('')
    loadFolder(folderStack[folderStack.length - 1].id)
  }

  function enterFolder(folder) {
    const next = [...folderStack, { id: folder.id, name: folder.name }]
    setFolderStack(next)
    setSearchInput('')
    setSearch('')
    loadFolder(folder.id)
  }

  function navigateTo(index) {
    const next = folderStack.slice(0, index + 1)
    setFolderStack(next)
    setSearchInput('')
    setSearch('')
    loadFolder(next[next.length - 1].id)
  }

  function toggleFile(id) {
    setSelected(prev => {
      const next = new Set(prev)
      next.has(id) ? next.delete(id) : next.add(id)
      return next
    })
  }

  function toggleAll() {
    if (selected.size === files.length) {
      setSelected(new Set())
    } else {
      setSelected(new Set(files.map(f => f.id)))
    }
  }

  async function handleImport() {
    setImporting(true)
    setImportResults([])
    const toImport = files.filter(f => selected.has(f.id))
    const results = []
    for (const f of toImport) {
      try {
        await axios.post(`${API_BASE}/gdrive/import`, { file_id: f.id, filename: f.name }, { withCredentials: true })
        results.push({ name: f.name, ok: true })
      } catch (err) {
        results.push({ name: f.name, ok: false, error: err.response?.data?.error || 'Failed' })
      }
    }
    setImportResults(results)
    setImporting(false)
    setSelected(new Set())
  }

  async function handleDisconnect() {
    try {
      await axios.delete(`${API_BASE}/gdrive/disconnect`, { withCredentials: true })
      setStatus({ ...status, connected: false })
      setFolders([])
      setFiles([])
    } catch (err) {
      setError(err.response?.data?.error || 'Failed to disconnect')
    }
  }

  const hasItems = folders.length > 0 || files.length > 0

  return (
    <>
      <Modal.Header closeButton>
        <Modal.Title>Google Drive</Modal.Title>
      </Modal.Header>
      <Modal.Body>
        {loading && <div className="text-center py-4"><Spinner animation="border" size="sm" className="me-2" />Checking connection...</div>}
        {!loading && error && <Alert variant="danger">{error}</Alert>}
        {!loading && status && !status.configured && (
          <Alert variant="warning">
            Google Drive is not configured. Set <code>GOOGLE_CLIENT_ID</code>, <code>GOOGLE_CLIENT_SECRET</code>,
            and <code>APP_BASE_URL</code> environment variables.
          </Alert>
        )}
        {!loading && status?.configured && !status.connected && (
          <div className="text-center py-3">
            <p className="text-muted mb-3">Connect your Google account to browse and import backup files from Drive.</p>
            <Button href={`${API_BASE}/gdrive/auth`} variant="primary">Connect Google Drive</Button>
          </div>
        )}
        {!loading && status?.connected && (
          <>
            {importResults.length > 0 && (
              <Alert variant={importResults.every(r => r.ok) ? 'success' : 'warning'} className="mb-3">
                {importResults.map((r, i) => (
                  <div key={i}>{r.ok ? '✓' : '✗'} {r.name}{!r.ok && ` — ${r.error}`}</div>
                ))}
              </Alert>
            )}

            {/* Breadcrumb — hidden during search */}
            {!search && (
              <nav aria-label="folder breadcrumb" className="mb-2">
                <ol className="breadcrumb mb-0 small">
                  {folderStack.map((crumb, i) => (
                    <li key={crumb.id} className={`breadcrumb-item${i === folderStack.length - 1 ? ' active' : ''}`}>
                      {i < folderStack.length - 1
                        ? <button className="btn btn-link p-0 text-decoration-none small" onClick={() => navigateTo(i)}>{crumb.name}</button>
                        : crumb.name
                      }
                    </li>
                  ))}
                </ol>
              </nav>
            )}

            <Form onSubmit={handleSearch} className="mb-3">
              <InputGroup>
                <Form.Control
                  placeholder="Search files across Drive…"
                  value={searchInput}
                  onChange={e => setSearchInput(e.target.value)}
                />
                {search
                  ? <Button variant="outline-secondary" onClick={clearSearch}>Clear</Button>
                  : <Button type="submit" variant="outline-secondary">Search</Button>
                }
              </InputGroup>
            </Form>

            {filesLoading && <div className="text-center py-3"><Spinner animation="border" size="sm" /></div>}
            {!filesLoading && !hasItems && (
              <p className="text-muted text-center py-2">{search ? `No files matching "${search}".` : 'This folder is empty.'}</p>
            )}
            {!filesLoading && hasItems && (
              <div style={{ maxHeight: '320px', overflowY: 'auto' }}>
                <table className="table table-sm table-hover mb-0">
                  <thead>
                    <tr>
                      <th style={{ width: '2rem' }}>
                        <Form.Check
                          checked={selected.size === files.length && files.length > 0}
                          indeterminate={selected.size > 0 && selected.size < files.length}
                          onChange={toggleAll}
                          disabled={files.length === 0}
                          title="Select all files"
                        />
                      </th>
                      <th>Name</th>
                      <th className="text-end">Size</th>
                      <th className="text-end">Modified</th>
                    </tr>
                  </thead>
                  <tbody>
                    {/* Folders first */}
                    {!search && folders.map(f => (
                      <tr key={f.id} onClick={() => enterFolder(f)} style={{ cursor: 'pointer' }} className="text-primary">
                        <td />
                        <td className="text-truncate" style={{ maxWidth: '280px' }}>
                          <svg width="14" height="14" fill="currentColor" className="me-1 mb-1" viewBox="0 0 16 16">
                            <path d="M.54 3.87.5 3a2 2 0 0 1 2-2h3.19a2 2 0 0 1 1.45.63l.33.38H14a2 2 0 0 1 2 2v6.5a2 2 0 0 1-2 2H2a2 2 0 0 1-2-1.99z"/>
                          </svg>
                          {f.name}
                        </td>
                        <td />
                        <td className="text-end text-muted small">{f.modifiedTime ? formatDate(f.modifiedTime) : '—'}</td>
                      </tr>
                    ))}
                    {/* Importable files */}
                    {files.map(f => (
                      <tr key={f.id} onClick={() => toggleFile(f.id)} style={{ cursor: 'pointer' }}>
                        <td><Form.Check checked={selected.has(f.id)} onChange={() => toggleFile(f.id)} onClick={e => e.stopPropagation()} /></td>
                        <td className="text-truncate" style={{ maxWidth: '280px' }}>{f.name}</td>
                        <td className="text-end text-muted small">{formatBytes(parseInt(f.size || 0))}</td>
                        <td className="text-end text-muted small">{f.modifiedTime ? formatDate(f.modifiedTime) : '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </>
        )}
      </Modal.Body>
      <Modal.Footer className="d-flex justify-content-between">
        <div>
          {status?.connected && (
            <Button variant="outline-danger" size="sm" onClick={handleDisconnect}>Disconnect</Button>
          )}
        </div>
        <div className="d-flex gap-2">
          {status?.connected && selected.size > 0 && (
            <Button size="sm" onClick={handleImport} disabled={importing}>
              {importing ? <><Spinner size="sm" className="me-1" />Importing…</> : `Import ${selected.size} file${selected.size !== 1 ? 's' : ''}`}
            </Button>
          )}
          <Button variant="secondary" size="sm" onClick={onBack}>Back</Button>
        </div>
      </Modal.Footer>
    </>
  )
}

// ── Root component ────────────────────────────────────────────────────────────

function WatchDirs({ onClose }) {
  const [browseDir, setBrowseDir] = useState(null)
  const [showGDrive, setShowGDrive] = useState(false)

  return (
    <Modal show onHide={onClose} size="lg" centered>
      {browseDir
        ? <BrowseView dir={browseDir} onBack={() => setBrowseDir(null)} />
        : showGDrive
          ? <GoogleDriveView onBack={() => setShowGDrive(false)} />
          : <SummaryView onClose={onClose} onBrowse={dir => setBrowseDir(dir)} onGDrive={() => setShowGDrive(true)} />
      }
    </Modal>
  )
}

export default WatchDirs

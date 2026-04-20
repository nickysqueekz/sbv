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

function SummaryView({ onClose, onBrowse }) {
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
  const [importing, setImporting] = useState({})   // path -> true
  const [results, setResults] = useState({})        // path -> {success, message}
  const PER_PAGE = 25

  const load = useCallback(async (p, s) => {
    setLoading(true)
    setError(null)
    try {
      const res = await axios.get(`${API_BASE}/watch-dirs/browse`, {
        params: { dir, page: p, per_page: PER_PAGE, search: s },
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

  useEffect(() => { load(1, '') }, [load])

  const handleSearch = (e) => {
    e.preventDefault()
    setSearch(searchInput)
    load(1, searchInput)
  }

  const handlePageChange = (p) => {
    load(p, search)
    setResults({})
  }

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
              <Button variant="outline-danger" onClick={() => { setSearchInput(''); setSearch(''); load(1, '') }}>✕</Button>
            )}
          </InputGroup>
        </Form>

        {loading && <div className="text-center py-3"><Spinner animation="border" size="sm" className="me-2" />Loading...</div>}
        {!loading && error && <Alert variant="danger">{error}</Alert>}
        {!loading && !error && files.length === 0 && (
          <Alert variant="warning">No files found{search ? ` matching "${search}"` : ''}.</Alert>
        )}

        {!loading && !error && files.length > 0 && (
          <table className="table table-sm table-hover align-middle mb-0">
            <thead className="table-light sticky-top">
              <tr>
                <th>Filename</th>
                <th className="text-end">Size</th>
                <th>Date</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {files.map(file => {
                const r = results[file.path]
                const busy = importing[file.path]
                return (
                  <tr key={file.path}>
                    <td className="text-truncate" style={{ maxWidth: 220 }} title={file.name}>
                      {file.name}
                      {r && <div className={`small ${r.success ? 'text-success' : 'text-danger'}`}>{r.message}</div>}
                    </td>
                    <td className="text-end text-nowrap text-muted small">{formatBytes(file.size)}</td>
                    <td className="text-nowrap text-muted small">{formatDate(file.modTime)}</td>
                    <td className="text-end">
                      <Button
                        size="sm"
                        variant={r?.success ? 'success' : 'primary'}
                        disabled={busy || r?.success}
                        onClick={() => handleImport(file)}
                      >
                        {busy ? <Spinner animation="border" size="sm" /> : r?.success ? '✓ Queued' : 'Import'}
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
          {total > 0 && `${total.toLocaleString()} file${total !== 1 ? 's' : ''}${search ? ` matching "${search}"` : ''} — newest first`}
        </span>
        <div className="d-flex align-items-center gap-2">
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

// ── Root component ────────────────────────────────────────────────────────────

function WatchDirs({ onClose }) {
  const [browseDir, setBrowseDir] = useState(null)

  return (
    <Modal show onHide={onClose} size="lg" centered>
      {browseDir
        ? <BrowseView dir={browseDir} onBack={() => setBrowseDir(null)} />
        : <SummaryView onClose={onClose} onBrowse={dir => setBrowseDir(dir)} />
      }
    </Modal>
  )
}

export default WatchDirs

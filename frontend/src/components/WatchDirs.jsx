import { useState, useEffect, useCallback } from 'react'
import axios from 'axios'
import { Modal, Button, Alert, Spinner, Badge, ListGroup } from 'react-bootstrap'

const API_BASE = import.meta.env.VITE_API_URL || 'http://localhost:8085/api'

function formatBytes(bytes) {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

function formatDate(dateStr) {
  return new Date(dateStr).toLocaleDateString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit'
  })
}

function WatchDirs({ onClose, onSuccess }) {
  const [files, setFiles] = useState([])
  const [watchDirs, setWatchDirs] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [importing, setImporting] = useState({}) // path -> true/false
  const [results, setResults] = useState({})     // path -> {success, message}

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await axios.get(`${API_BASE}/watch-dirs`, { withCredentials: true })
      setWatchDirs(res.data.watch_dirs || [])
      setFiles(res.data.files || [])
    } catch (err) {
      setError(err.response?.data?.error || 'Failed to load watch directories')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const handleImport = async (file) => {
    setImporting(prev => ({ ...prev, [file.path]: true }))
    setResults(prev => ({ ...prev, [file.path]: null }))
    try {
      const res = await axios.post(
        `${API_BASE}/watch-dirs/import`,
        { path: file.path },
        { withCredentials: true }
      )
      setResults(prev => ({ ...prev, [file.path]: { success: true, message: res.data.message } }))
      if (onSuccess) onSuccess()
    } catch (err) {
      setResults(prev => ({
        ...prev,
        [file.path]: { success: false, message: err.response?.data?.error || 'Import failed' }
      }))
    } finally {
      setImporting(prev => ({ ...prev, [file.path]: false }))
    }
  }

  const groupedByDir = files.reduce((acc, f) => {
    if (!acc[f.dir]) acc[f.dir] = []
    acc[f.dir].push(f)
    return acc
  }, {})

  return (
    <Modal show onHide={onClose} size="lg" centered>
      <Modal.Header closeButton>
        <Modal.Title>Browse Backup Files</Modal.Title>
      </Modal.Header>
      <Modal.Body>
        {loading && (
          <div className="text-center py-4">
            <Spinner animation="border" size="sm" className="me-2" />
            Loading...
          </div>
        )}

        {!loading && error && (
          <Alert variant="danger">{error}</Alert>
        )}

        {!loading && !error && watchDirs.length === 0 && (
          <Alert variant="info">
            No watch directories configured. Set the <code>WATCH_DIRS</code> environment
            variable to a comma-separated list of container paths containing your backup XMLs.
          </Alert>
        )}

        {!loading && !error && watchDirs.length > 0 && files.length === 0 && (
          <Alert variant="warning">
            No XML files found in configured watch directories:
            <ul className="mb-0 mt-1">
              {watchDirs.map(d => <li key={d}><code>{d}</code></li>)}
            </ul>
          </Alert>
        )}

        {!loading && !error && Object.entries(groupedByDir).map(([dir, dirFiles]) => (
          <div key={dir} className="mb-3">
            <div className="text-muted small mb-2">
              <code>{dir}</code>
            </div>
            <ListGroup>
              {dirFiles.map(file => {
                const result = results[file.path]
                const isImporting = importing[file.path]
                const alreadyQueued = result?.success
                return (
                  <ListGroup.Item key={file.path} className="d-flex align-items-center gap-3">
                    <div className="flex-grow-1 min-w-0">
                      <div className="fw-medium text-truncate" title={file.name}>
                        {file.name}
                      </div>
                      <div className="text-muted small">
                        {formatBytes(file.size)} &middot; {formatDate(file.modTime)}
                      </div>
                      {result && (
                        <div className={`small mt-1 ${result.success ? 'text-success' : 'text-danger'}`}>
                          {result.message}
                        </div>
                      )}
                    </div>
                    <Button
                      size="sm"
                      variant={alreadyQueued ? 'success' : 'primary'}
                      disabled={isImporting || alreadyQueued}
                      onClick={() => handleImport(file)}
                    >
                      {isImporting ? (
                        <><Spinner animation="border" size="sm" className="me-1" />Queuing...</>
                      ) : alreadyQueued ? (
                        'Queued'
                      ) : (
                        'Import'
                      )}
                    </Button>
                  </ListGroup.Item>
                )
              })}
            </ListGroup>
          </div>
        ))}
      </Modal.Body>
      <Modal.Footer>
        <Button variant="secondary" onClick={onClose}>Close</Button>
        {!loading && <Button variant="outline-secondary" size="sm" onClick={load}>Refresh</Button>}
      </Modal.Footer>
    </Modal>
  )
}

export default WatchDirs

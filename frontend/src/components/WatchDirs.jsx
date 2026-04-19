import { useState, useEffect, useCallback } from 'react'
import axios from 'axios'
import { Modal, Button, Alert, Spinner, Card, Row, Col } from 'react-bootstrap'

const API_BASE = import.meta.env.VITE_API_URL || 'http://localhost:8085/api'

function formatBytes(bytes) {
  if (!bytes || bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

function WatchDirs({ onClose, onSuccess }) {
  const [summaries, setSummaries] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [importing, setImporting] = useState(false)
  const [result, setResult] = useState(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await axios.get(`${API_BASE}/watch-dirs`, { withCredentials: true })
      setSummaries(res.data.watch_dirs || [])
    } catch (err) {
      setError(err.response?.data?.error || 'Failed to load watch directories')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const totalFiles = summaries.reduce((n, s) => n + (s.total_files || 0), 0)
  const totalSize = summaries.reduce((n, s) => n + (s.total_size || 0), 0)

  const handleImportAll = async () => {
    setImporting(true)
    setResult(null)
    try {
      const res = await axios.post(`${API_BASE}/watch-dirs/import-all`, {}, { withCredentials: true })
      setResult({ success: true, message: res.data.message })
      if (onSuccess) onSuccess()
    } catch (err) {
      setResult({ success: false, message: err.response?.data?.error || 'Import failed' })
    } finally {
      setImporting(false)
    }
  }

  const noFiles = !loading && !error && totalFiles === 0
  const hasFiles = !loading && !error && totalFiles > 0

  return (
    <Modal show onHide={onClose} size="lg" centered>
      <Modal.Header closeButton>
        <Modal.Title>Import Backup Files</Modal.Title>
      </Modal.Header>
      <Modal.Body>
        {loading && (
          <div className="text-center py-4">
            <Spinner animation="border" size="sm" className="me-2" />
            Scanning directories...
          </div>
        )}

        {!loading && error && (
          <Alert variant="danger">{error}</Alert>
        )}

        {!loading && !error && summaries.length === 0 && (
          <Alert variant="info">
            No watch directories configured. Set the <code>WATCH_DIRS</code> environment
            variable to a comma-separated list of container paths.
          </Alert>
        )}

        {noFiles && summaries.length > 0 && (
          <Alert variant="warning">
            No XML files found in configured watch directories.
          </Alert>
        )}

        {hasFiles && (
          <>
            <Row className="g-3 mb-3">
              {summaries.map(s => (
                <Col key={s.dir} sm={6}>
                  <Card className="h-100">
                    <Card.Body>
                      <Card.Subtitle className="text-muted small mb-1">
                        <code>{s.dir}</code>
                      </Card.Subtitle>
                      <div className="fw-semibold fs-5">{s.total_files.toLocaleString()} files</div>
                      <div className="text-muted small">{formatBytes(s.total_size)}</div>
                    </Card.Body>
                  </Card>
                </Col>
              ))}
            </Row>

            <Alert variant="secondary" className="mb-3">
              <strong>{totalFiles.toLocaleString()} XML files</strong> ({formatBytes(totalSize)}) ready to import.
              Files already queued will be skipped. Duplicate messages are ignored automatically.
              Processing runs every 60 seconds after queuing.
            </Alert>
          </>
        )}

        {result && (
          <Alert variant={result.success ? 'success' : 'danger'} className="mb-0">
            {result.message}
          </Alert>
        )}
      </Modal.Body>
      <Modal.Footer>
        <Button variant="secondary" onClick={onClose}>Close</Button>
        {!loading && <Button variant="outline-secondary" size="sm" onClick={load}>Refresh</Button>}
        {hasFiles && !result?.success && (
          <Button
            variant="primary"
            onClick={handleImportAll}
            disabled={importing}
          >
            {importing ? (
              <><Spinner animation="border" size="sm" className="me-2" />Queuing {totalFiles.toLocaleString()} files...</>
            ) : (
              `Import All ${totalFiles.toLocaleString()} Files`
            )}
          </Button>
        )}
      </Modal.Footer>
    </Modal>
  )
}

export default WatchDirs

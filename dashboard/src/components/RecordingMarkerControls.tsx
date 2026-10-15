import { useState } from 'react'

interface RecordingMarkerControlsProps {
  openTransactions: string[]
  // Each returns whether the marker was added, which clears its input.
  onStartTransaction: (name: string) => Promise<boolean>
  onEndTransaction: (name: string) => void
  onAddNote: (text: string) => Promise<boolean>
  // Reports an empty name or note, which is not sent.
  onInvalid: (message: string) => void
}

/**
 * Transaction and note markers for a live recording, shared by the browser
 * and HTTP proxy recorders: name a transaction as you begin a step, end it
 * when the step is done, and leave notes for the converted script.
 */
export default function RecordingMarkerControls({
  openTransactions,
  onStartTransaction,
  onEndTransaction,
  onAddNote,
  onInvalid,
}: RecordingMarkerControlsProps) {
  const [transactionName, setTransactionName] = useState('')
  const [noteText, setNoteText] = useState('')

  const startTransaction = async () => {
    const name = transactionName.trim()
    if (!name) {
      onInvalid('Enter a transaction name')
      return
    }
    if (await onStartTransaction(name)) setTransactionName('')
  }

  const addNote = async () => {
    const text = noteText.trim()
    if (!text) {
      onInvalid('Enter a note')
      return
    }
    if (await onAddNote(text)) setNoteText('')
  }

  return (
    <div className="brp-markers">
      <div className="brp-row">
        <input
          className="rec-input"
          aria-label="Transaction name"
          value={transactionName}
          onChange={e => setTransactionName(e.target.value)}
          placeholder="Transaction name, e.g. Checkout"
        />
        <button type="button" className="btn btn-secondary btn-sm" onClick={startTransaction}>Start transaction</button>
      </div>
      {openTransactions.length > 0 && (
        <div className="brp-open-transactions" aria-label="Open transactions">
          <div className="brp-open-transactions-title">Open transactions</div>
          {openTransactions.map(name => (
            <div key={name} className="brp-txn-row" data-testid="open-transaction">
              <span className="brp-txn-name">{name}</span>
              <button
                type="button"
                className="btn btn-secondary btn-sm brp-txn-end"
                onClick={() => onEndTransaction(name)}
                aria-label={`End transaction ${name}`}
              >
                End transaction
              </button>
            </div>
          ))}
        </div>
      )}
      <div className="brp-row">
        <input
          className="rec-input"
          aria-label="Note"
          value={noteText}
          onChange={e => setNoteText(e.target.value)}
          placeholder="Add a note for the next action…"
        />
        <button type="button" className="btn btn-secondary btn-sm" onClick={addNote}>Add note</button>
      </div>
    </div>
  )
}

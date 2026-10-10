package generatorrpc

import "context"

// sharedClient holds a weak canonical handle: only returned handles contribute
// to the reference count. The last user closes and evicts the transport, so
// idle/disconnected generator identities cannot accumulate cached clients.
func (r *ReverseRegistry) sharedClient(ctx context.Context, generatorID string, create func() (*Client, error)) (*Client, error) {
	r.clientsMu.Lock()
	defer r.clientsMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if current := r.clients[generatorID]; current != nil {
		if retained, err := current.Retain(); err == nil {
			return retained, nil
		}
	}
	client, err := create()
	if err != nil {
		return nil, err
	}
	canonical := &Client{conn: client.conn, rpc: client.rpc, connection: client.connection}
	client.connection.onClose = func() {
		r.clientsMu.Lock()
		defer r.clientsMu.Unlock()
		if r.clients[generatorID] == canonical {
			delete(r.clients, generatorID)
		}
	}
	if r.clients == nil {
		r.clients = make(map[string]*Client)
	}
	r.clients[generatorID] = canonical
	return client, nil
}

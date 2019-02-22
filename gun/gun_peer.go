package gun

import (
	"context"
	"sync"
	"time"
)

type gunPeer struct {
	url        string
	connPeer   func() (Peer, error)
	sleepOnErr time.Duration // TODO: would be better as backoff
	id         string

	peer     Peer
	peerBad  bool // If true, don't try anything
	peerLock sync.Mutex
}

func newGunPeer(url string, connPeer func() (Peer, error), sleepOnErr time.Duration) (*gunPeer, error) {
	p := &gunPeer{url: url, connPeer: connPeer, sleepOnErr: sleepOnErr}
	var err error
	if p.peer, err = connPeer(); err != nil {
		return nil, err
	}
	return p, nil
}

func (g *gunPeer) ID() string { return g.id }

func (g *gunPeer) reconnectPeer() (err error) {
	g.peerLock.Lock()
	defer g.peerLock.Unlock()
	if g.peer == nil && g.peerBad {
		g.peerBad = false
		if g.peer, err = g.connPeer(); err != nil {
			g.peerBad = true
			time.AfterFunc(g.sleepOnErr, func() { g.reconnectPeer() })
		}
	}
	return
}

// Can be nil peer if currently bad
func (g *gunPeer) connectedPeer() Peer {
	g.peerLock.Lock()
	defer g.peerLock.Unlock()
	return g.peer
}

func (g *gunPeer) markPeerErrored(p Peer) {
	g.peerLock.Lock()
	defer g.peerLock.Unlock()
	if p == g.peer {
		g.peer = nil
		g.peerBad = true
		p.Close()
		time.AfterFunc(g.sleepOnErr, func() { g.reconnectPeer() })
	}
}

func (g *gunPeer) send(ctx context.Context, msg *Message, moreMsgs ...*Message) (ok bool, err error) {
	p := g.connectedPeer()
	if p == nil {
		return false, nil
	}
	// Clone them with peer "to"
	updatedMsg := msg.Clone()
	updatedMsg.To = g.url
	updatedMoreMsgs := make([]*Message, len(moreMsgs))
	for i, moreMsg := range moreMsgs {
		moreMsg := moreMsg.Clone()
		moreMsg.To = g.url
		updatedMoreMsgs[i] = moreMsg
	}
	if err = p.Send(ctx, updatedMsg, updatedMoreMsgs...); err != nil {
		g.markPeerErrored(p)
		return false, err
	} else {
		return true, nil
	}
}

func (g *gunPeer) receive(ctx context.Context) (ok bool, msgs []*Message, err error) {
	if p := g.connectedPeer(); p == nil {
		return false, nil, nil
	} else if msgs, err = p.Receive(ctx); err != nil {
		g.markPeerErrored(p)
		return false, nil, err
	} else {
		return true, msgs, nil
	}
}

func (g *gunPeer) Close() error {
	g.peerLock.Lock()
	defer g.peerLock.Unlock()
	var err error
	if g.peer != nil {
		err = g.peer.Close()
		g.peer = nil
	}
	g.peerBad = false
	return err
}

func (g *gunPeer) closed() bool {
	g.peerLock.Lock()
	defer g.peerLock.Unlock()
	return g.peer == nil && !g.peerBad
}

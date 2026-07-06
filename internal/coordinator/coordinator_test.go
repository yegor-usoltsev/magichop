package coordinator

import (
	"testing"

	"github.com/nats-io/nats-server/v2/server"

	"github.com/yegor-usoltsev/MagicHop/internal/protocol"
)

func TestCountCommandSubscribersDedupesNodeConnections(t *testing.T) {
	t.Parallel()

	conns := []*server.ConnInfo{
		{Cid: 1, Name: "magichop-mac-a", Subs: []string{protocol.SubjectCommandsBroadcast}},
		{Cid: 2, Name: "magichop-mac-b", Subs: []string{protocol.SubjectCommandsBroadcast}},
		{Cid: 3, Name: "magichop-mac-b", Subs: []string{protocol.SubjectCommandsBroadcast}},
		{Cid: 4, Name: "magichop-mac-c", Subs: []string{"other.subject"}},
		{Cid: 5, Name: "magichop-claim", Subs: nil},
	}

	if got := countCommandSubscribers(conns, "mac-a"); got != 1 {
		t.Fatalf("countCommandSubscribers = %d, want 1", got)
	}
}

func TestCountCommandSubscribersCountsUnnamedConnectionsIndividually(t *testing.T) {
	t.Parallel()

	conns := []*server.ConnInfo{
		{Cid: 1, Subs: []string{protocol.SubjectCommandsBroadcast}},
		{Cid: 2, Subs: []string{protocol.SubjectCommandsBroadcast}},
	}

	if got := countCommandSubscribers(conns, "mac-a"); got != 2 {
		t.Fatalf("countCommandSubscribers = %d, want 2", got)
	}
}

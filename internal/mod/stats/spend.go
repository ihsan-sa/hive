package stats

import (
	"fmt"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// SpendNotice tells the person once a month when runs have cost more than the
// monthly figure they set (monthly_usd). It is a notice in the feed: work goes
// on. Once a month holds because Person.Get finds it by its ID. It was the
// core's until docs/core.md's first move: the core keeps what runs cost
// (Runs.Spent) and the person's feed; saying something about it is a module's.
type SpendNotice struct{ C *core.Core }

// Tick is the notice's step on the module tick list.
func (n SpendNotice) Tick() { n.tell(n.C.WS.LoadConfig().MonthlyUSD) }

func (n SpendNotice) tell(limit float64) {
	month := time.Now().UTC().Format("2006-01")
	id := "spend-" + month
	if spent := n.C.Runs.Spent(month); limit <= 0 || spent <= limit {
		return
	} else if _, told := n.C.Person.Get(id); !told {
		n.C.Person.Tell(core.Ask{ID: id,
			Title:  fmt.Sprintf("Runs have cost $%.2f this month, past your $%.2f", spent, limit),
			Detail: "Work goes on; this is the one notice for " + month + ". The figure is monthly_usd in .hive/config.json."})
	}
}

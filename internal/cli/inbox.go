package cli

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"github.com/google/uuid"
)

func (a *app) agentInbox(action string, args []string) error {
	f := a.flags("agent " + action)
	var p manager.InboxRequest
	f.StringVar(&p.ID, "id", "", "registered contact name")
	f.StringVar(&p.Session, "session", "", "exact session UUID returned by registration")
	f.StringVar(&p.Registration, "registration", "", "optional registration identity to pin")
	reading := action == "inbox" || action == "watch"
	if reading {
		f.StringVar(&p.After, "after", "", "resume after an opaque inbox cursor")
		f.BoolVar(&p.Pending, "pending", false, "return only messages without an explicit acknowledgement")
	}
	defaultWait := time.Duration(0)
	if action == "watch" {
		defaultWait = 20 * time.Second
	}
	if reading {
		f.DurationVar(&p.Wait, "wait", defaultWait, "maximum long-poll wait, 0..20s")
	}
	once := new(bool)
	if action == "watch" {
		f.BoolVar(once, "once", false, "watch one response batch and exit")
	}
	message, text := new(string), new(string)
	if !reading {
		f.StringVar(message, "message", "", "required message ID")
	}
	if action == "ack" {
		f.StringVar(text, "text", "", "response text, routed to the original agent sender inbox or operator")
	}
	f.Bool("json", false, "JSON output (watch emits one JSON object per message)")
	if err := a.parse(f, args, false); err != nil {
		return err
	}
	if len(f.Args()) != 0 || p.ID == "" || p.Session == "" {
		return model.Err("invalid_request", "provide --id and the exact --session; positional arguments are not accepted")
	}
	id, err := uuid.Parse(p.Session)
	if err != nil {
		return model.Err("invalid_request", "session must be an exact UUID")
	}
	p.Session = id.String()
	if p.Wait < 0 || p.Wait > 20*time.Second {
		return model.Err("invalid_request", "wait must be between 0 and 20s")
	}
	if action == "receive" || action == "ack" {
		if *message == "" {
			return model.Err("invalid_request", "receive/ack requires --message")
		}
		var receipt any
		if err := a.call("agent_receipt", manager.ReceiptRequest{ID: p.ID, Session: p.Session, Registration: p.Registration, Message: *message, Acknowledge: action == "ack", Text: *text}, &receipt); err != nil {
			return err
		}
		return a.print(receipt)
	}
	if *message != "" || *text != "" {
		return model.Err("invalid_request", "message/text apply only to receive or ack")
	}
	if action == "inbox" {
		var page manager.InboxPage
		if err := a.call("agent_inbox", p, &page); err != nil {
			return err
		}
		return a.print(page)
	}
	if p.Wait == 0 && !*once {
		return model.Err("invalid_request", "continuous watch requires a positive wait to avoid busy polling")
	}
	backoff := 200 * time.Millisecond
	reconnecting := false
	for {
		ctx, cancel := context.WithTimeout(a.ctx, p.Wait+5*time.Second)
		var page manager.InboxPage
		err := client.Call(ctx, a.home, "agent_inbox", p, &page)
		cancel()
		if err != nil {
			if a.ctx.Err() != nil {
				return a.ctx.Err()
			}
			var api *model.Error
			if errors.As(err, &api) && api.Code != "backend_stopping" && api.Code != "inbox_busy" {
				return err
			}
			if *once {
				return err
			}
			if !reconnecting {
				a.notice("ARM inbox disconnected; reconnecting with the last emitted cursor")
				reconnecting = true
			}
			select {
			case <-a.ctx.Done():
				return a.ctx.Err()
			case <-time.After(backoff):
			}
			backoff = min(5*time.Second, backoff*2)
			continue
		}
		if reconnecting {
			a.notice("ARM inbox reconnected")
			reconnecting = false
		}
		backoff = 200 * time.Millisecond
		if p.Registration != "" && p.Registration != page.Registration {
			return model.Err("contact_changed", "watch recipient registration changed")
		}
		p.Registration = page.Registration
		for _, item := range page.Items {
			line := struct {
				Host         string `json:"host"`
				ID           string `json:"id"`
				Session      string `json:"session"`
				Registration string `json:"registration"`
				manager.InboxItem
			}{page.Host, page.ID, page.Session, page.Registration, item}
			if a.machine() {
				err = json.NewEncoder(a.out).Encode(line)
			} else {
				err = a.print(line)
			}
			if err != nil {
				return err
			}
			p.After = item.Cursor // advance only after a successful stdout write
		}
		p.After = page.Cursor
		if *once {
			return nil
		}
	}
}

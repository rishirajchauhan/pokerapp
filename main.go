package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	maxPlayers = 6
	startChips = 1000
	smallBlind = 10
	bigBlind   = 20
)

type Card struct {
	Rank int    `json:"rank"`
	Suit string `json:"suit"`
}

func (c Card) String() string {
	ranks := map[int]string{
		2:  "2",
		3:  "3",
		4:  "4",
		5:  "5",
		6:  "6",
		7:  "7",
		8:  "8",
		9:  "9",
		10: "10",
		11: "J",
		12: "Q",
		13: "K",
		14: "A",
	}
	return ranks[c.Rank] + c.Suit
}

type Player struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Seat       int    `json:"seat"`
	Chips      int    `json:"chips"`
	Hand       []Card `json:"hand"`
	Bet        int    `json:"bet"`
	Folded     bool   `json:"folded"`
	HasActed   bool   `json:"hasActed"`
	IsDealer   bool   `json:"isDealer"`
	IsTurn     bool   `json:"isTurn"`
	LastResult string `json:"lastResult,omitempty"`
}

type Subscriber struct {
	PlayerID string
	Updates  chan struct{}
}

type Table struct {
	mu            sync.Mutex
	players       []*Player
	deck          []Card
	community     []Card
	pot           int
	currentBet    int
	dealerIndex   int
	turnIndex     int
	street        string
	status        string
	message       string
	winners       []string
	subscribers   map[string]*Subscriber
	subscribeSeq  int
	lastActionLog []string
}

type tableView struct {
	Players     []playerView `json:"players"`
	Community   []string     `json:"community"`
	Pot         int          `json:"pot"`
	CurrentBet  int          `json:"currentBet"`
	Street      string       `json:"street"`
	Status      string       `json:"status"`
	Message     string       `json:"message"`
	Winners     []string     `json:"winners"`
	LastActions []string     `json:"lastActions"`
	ViewerID    string       `json:"viewerId"`
}

type playerView struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Seat       int      `json:"seat"`
	Chips      int      `json:"chips"`
	Hand       []string `json:"hand"`
	Bet        int      `json:"bet"`
	Folded     bool     `json:"folded"`
	HasActed   bool     `json:"hasActed"`
	IsDealer   bool     `json:"isDealer"`
	IsTurn     bool     `json:"isTurn"`
	LastResult string   `json:"lastResult,omitempty"`
}

type joinRequest struct {
	Name string `json:"name"`
}

type joinResponse struct {
	PlayerID string    `json:"playerId"`
	State    tableView `json:"state"`
}

type actionRequest struct {
	PlayerID string `json:"playerId"`
	Action   string `json:"action"`
}

type startRequest struct {
	PlayerID string `json:"playerId"`
}

func newTable() *Table {
	return &Table{
		dealerIndex: -1,
		turnIndex:   -1,
		street:      "waiting",
		status:      "waiting",
		message:     "Waiting for at least 2 players.",
		subscribers: make(map[string]*Subscriber),
	}
}

func main() {
	table := newTable()
	mux := http.NewServeMux()

	staticDir := filepath.Join(".", "static")
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))
	mux.HandleFunc("/api/join", table.handleJoin)
	mux.HandleFunc("/api/start", table.handleStart)
	mux.HandleFunc("/api/action", table.handleAction)
	mux.HandleFunc("/api/state", table.handleState)
	mux.HandleFunc("/api/events", table.handleEvents)

	addr := ":8080"
	log.Printf("Poker server listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func (t *Table) handleJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req joinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.status == "playing" {
		http.Error(w, "wait for the current hand to finish before joining", http.StatusConflict)
		return
	}

	if len(t.players) >= maxPlayers {
		http.Error(w, "table is full", http.StatusConflict)
		return
	}

	player := &Player{
		ID:    randomID(),
		Name:  name,
		Seat:  len(t.players) + 1,
		Chips: startChips,
	}
	t.players = append(t.players, player)
	t.message = fmt.Sprintf("%s joined the table.", player.Name)
	view := t.viewForLocked(player.ID)
	t.broadcastLocked()

	writeJSON(w, joinResponse{
		PlayerID: player.ID,
		State:    view,
	})
}

func (t *Table) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	player := t.findPlayerLocked(req.PlayerID)
	if player == nil {
		http.Error(w, "unknown player", http.StatusNotFound)
		return
	}

	if err := t.startHandLocked(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]string{"status": "ok"})
}

func (t *Table) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req actionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.applyActionLocked(req.PlayerID, req.Action); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]string{"status": "ok"})
}

func (t *Table) handleState(w http.ResponseWriter, r *http.Request) {
	playerID := r.URL.Query().Get("player_id")

	t.mu.Lock()
	defer t.mu.Unlock()

	writeJSON(w, t.viewForLocked(playerID))
}

func (t *Table) handleEvents(w http.ResponseWriter, r *http.Request) {
	playerID := r.URL.Query().Get("player_id")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	subID, subscriber := t.subscribe(playerID)
	defer t.unsubscribe(subID)

	t.mu.Lock()
	initial, err := json.Marshal(t.viewForLocked(playerID))
	t.mu.Unlock()
	if err != nil {
		http.Error(w, "failed to encode state", http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "data: %s\n\n", initial)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-subscriber.Updates:
			t.mu.Lock()
			payload, err := json.Marshal(t.viewForLocked(playerID))
			t.mu.Unlock()
			if err != nil {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

func (t *Table) subscribe(playerID string) (string, *Subscriber) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.subscribeSeq++
	id := fmt.Sprintf("sub-%d", t.subscribeSeq)
	sub := &Subscriber{
		PlayerID: playerID,
		Updates:  make(chan struct{}, 1),
	}
	t.subscribers[id] = sub
	return id, sub
}

func (t *Table) unsubscribe(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.subscribers, id)
}

func (t *Table) broadcastLocked() {
	for _, sub := range t.subscribers {
		select {
		case sub.Updates <- struct{}{}:
		default:
		}
	}
}

func (t *Table) startHandLocked() error {
	if len(t.players) < 2 {
		return fmt.Errorf("need at least 2 players to start")
	}
	if t.status == "playing" {
		return fmt.Errorf("a hand is already running")
	}

	t.resetRoundStateLocked()
	t.deck = shuffledDeck()
	t.community = nil
	t.pot = 0
	t.currentBet = bigBlind
	t.status = "playing"
	t.street = "preflop"
	t.winners = nil
	t.lastActionLog = nil

	t.dealerIndex = nextOccupiedIndex(t.players, t.dealerIndex)
	for i, player := range t.players {
		player.Hand = []Card{t.drawLocked(), t.drawLocked()}
		player.Bet = 0
		player.Folded = false
		player.HasActed = false
		player.IsDealer = i == t.dealerIndex
		player.IsTurn = false
		player.LastResult = ""
	}

	sbIndex := nextOccupiedIndex(t.players, t.dealerIndex)
	bbIndex := nextOccupiedIndex(t.players, sbIndex)
	t.postBlindLocked(sbIndex, smallBlind)
	t.postBlindLocked(bbIndex, bigBlind)

	t.players[sbIndex].HasActed = false
	t.players[bbIndex].HasActed = false
	t.turnIndex = nextOccupiedIndex(t.players, bbIndex)
	t.players[t.turnIndex].IsTurn = true
	t.message = fmt.Sprintf("%s started a new hand.", t.players[t.dealerIndex].Name)
	t.broadcastLocked()
	return nil
}

func (t *Table) applyActionLocked(playerID, action string) error {
	if t.status != "playing" {
		return fmt.Errorf("no hand is running")
	}
	if t.turnIndex < 0 || t.turnIndex >= len(t.players) {
		return fmt.Errorf("invalid turn state")
	}

	player := t.players[t.turnIndex]
	if player.ID != playerID {
		return fmt.Errorf("it is not your turn")
	}

	switch action {
	case "fold":
		player.Folded = true
		player.HasActed = true
		t.addLogLocked("%s folds.", player.Name)
	case "check":
		if player.Bet != t.currentBet {
			return fmt.Errorf("you must call before you can check")
		}
		player.HasActed = true
		t.addLogLocked("%s checks.", player.Name)
	case "call":
		need := t.currentBet - player.Bet
		if need <= 0 {
			player.HasActed = true
			t.addLogLocked("%s checks.", player.Name)
			break
		}
		if player.Chips < need {
			need = player.Chips
		}
		player.Chips -= need
		player.Bet += need
		t.pot += need
		player.HasActed = true
		t.addLogLocked("%s calls %d.", player.Name, need)
	default:
		return fmt.Errorf("unsupported action: %s", action)
	}

	if t.resolveIfOnePlayerLeftLocked() {
		return nil
	}

	if t.roundCompleteLocked() {
		t.advanceStreetLocked()
		return nil
	}

	t.advanceTurnLocked()
	t.broadcastLocked()
	return nil
}

func (t *Table) resolveIfOnePlayerLeftLocked() bool {
	var active []*Player
	for _, player := range t.players {
		if !player.Folded && len(player.Hand) > 0 {
			active = append(active, player)
		}
	}
	if len(active) != 1 {
		return false
	}

	winner := active[0]
	winner.Chips += t.pot
	winner.LastResult = fmt.Sprintf("Won %d chips", t.pot)
	t.winners = []string{winner.Name}
	t.status = "finished"
	t.street = "showdown"
	t.message = fmt.Sprintf("%s wins because everyone else folded.", winner.Name)
	t.turnIndex = -1
	t.clearTurnMarkersLocked()
	t.broadcastLocked()
	return true
}

func (t *Table) roundCompleteLocked() bool {
	for _, player := range t.players {
		if player.Folded || len(player.Hand) == 0 {
			continue
		}
		if player.Chips == 0 && player.Bet <= t.currentBet {
			continue
		}
		if !player.HasActed || player.Bet != t.currentBet {
			return false
		}
	}
	return true
}

func (t *Table) advanceStreetLocked() {
	switch t.street {
	case "preflop":
		t.community = append(t.community, t.drawLocked(), t.drawLocked(), t.drawLocked())
		t.street = "flop"
		t.message = "Flop dealt."
	case "flop":
		t.community = append(t.community, t.drawLocked())
		t.street = "turn"
		t.message = "Turn dealt."
	case "turn":
		t.community = append(t.community, t.drawLocked())
		t.street = "river"
		t.message = "River dealt."
	case "river":
		t.finishShowdownLocked()
		return
	}

	for _, player := range t.players {
		player.Bet = 0
		player.HasActed = false
	}
	t.currentBet = 0
	t.turnIndex = nextActiveIndex(t.players, t.dealerIndex)
	t.clearTurnMarkersLocked()
	if t.turnIndex >= 0 {
		t.players[t.turnIndex].IsTurn = true
	}
	t.broadcastLocked()
}

func (t *Table) finishShowdownLocked() {
	type scoredPlayer struct {
		player *Player
		score  handScore
	}

	var scored []scoredPlayer
	for _, player := range t.players {
		if player.Folded || len(player.Hand) == 0 {
			continue
		}
		score := bestHand(append(append([]Card{}, player.Hand...), t.community...))
		scored = append(scored, scoredPlayer{player: player, score: score})
		player.LastResult = score.label()
	}

	if len(scored) == 0 {
		t.status = "finished"
		t.street = "showdown"
		t.message = "Hand ended with no eligible players."
		t.broadcastLocked()
		return
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score.compare(scored[j].score) > 0
	})

	best := scored[0].score
	var winners []*Player
	for _, candidate := range scored {
		if candidate.score.compare(best) == 0 {
			winners = append(winners, candidate.player)
		}
	}

	share := t.pot / len(winners)
	remainder := t.pot % len(winners)
	t.winners = nil
	for i, winner := range winners {
		payout := share
		if i < remainder {
			payout++
		}
		winner.Chips += payout
		winner.LastResult = fmt.Sprintf("%s, won %d chips", best.label(), payout)
		t.winners = append(t.winners, winner.Name)
	}

	t.status = "finished"
	t.street = "showdown"
	t.turnIndex = -1
	t.clearTurnMarkersLocked()
	t.message = fmt.Sprintf("Showdown: %s", strings.Join(t.winners, ", "))
	t.broadcastLocked()
}

func (t *Table) advanceTurnLocked() {
	t.clearTurnMarkersLocked()
	t.turnIndex = nextActiveIndex(t.players, t.turnIndex)
	if t.turnIndex >= 0 {
		t.players[t.turnIndex].IsTurn = true
		t.message = fmt.Sprintf("%s to act.", t.players[t.turnIndex].Name)
	}
}

func (t *Table) clearTurnMarkersLocked() {
	for _, player := range t.players {
		player.IsTurn = false
	}
}

func (t *Table) postBlindLocked(index, amount int) {
	player := t.players[index]
	if player.Chips < amount {
		amount = player.Chips
	}
	player.Chips -= amount
	player.Bet += amount
	t.pot += amount
}

func (t *Table) resetRoundStateLocked() {
	for _, player := range t.players {
		player.Hand = nil
		player.Bet = 0
		player.Folded = false
		player.HasActed = false
		player.IsDealer = false
		player.IsTurn = false
		player.LastResult = ""
	}
}

func (t *Table) viewForLocked(playerID string) tableView {
	view := tableView{
		Pot:         t.pot,
		CurrentBet:  t.currentBet,
		Street:      t.street,
		Status:      t.status,
		Message:     t.message,
		Winners:     append([]string{}, t.winners...),
		LastActions: append([]string{}, t.lastActionLog...),
		ViewerID:    playerID,
	}

	for _, player := range t.players {
		pv := playerView{
			ID:         player.ID,
			Name:       player.Name,
			Seat:       player.Seat,
			Chips:      player.Chips,
			Bet:        player.Bet,
			Folded:     player.Folded,
			HasActed:   player.HasActed,
			IsDealer:   player.IsDealer,
			IsTurn:     player.IsTurn,
			LastResult: player.LastResult,
		}

		switch {
		case player.ID == playerID:
			for _, card := range player.Hand {
				pv.Hand = append(pv.Hand, card.String())
			}
		case t.status == "finished":
			for _, card := range player.Hand {
				pv.Hand = append(pv.Hand, card.String())
			}
		default:
			for range player.Hand {
				pv.Hand = append(pv.Hand, "??")
			}
		}

		view.Players = append(view.Players, pv)
	}

	for _, card := range t.community {
		view.Community = append(view.Community, card.String())
	}

	return view
}

func (t *Table) findPlayerLocked(id string) *Player {
	for _, player := range t.players {
		if player.ID == id {
			return player
		}
	}
	return nil
}

func (t *Table) addLogLocked(format string, args ...any) {
	entry := fmt.Sprintf(format, args...)
	t.lastActionLog = append([]string{entry}, t.lastActionLog...)
	if len(t.lastActionLog) > 8 {
		t.lastActionLog = t.lastActionLog[:8]
	}
}

func nextOccupiedIndex(players []*Player, current int) int {
	if len(players) == 0 {
		return -1
	}
	index := current
	for i := 0; i < len(players); i++ {
		index = (index + 1) % len(players)
		if players[index].Chips > 0 || len(players[index].Hand) == 0 {
			return index
		}
	}
	return -1
}

func nextActiveIndex(players []*Player, current int) int {
	if len(players) == 0 {
		return -1
	}
	index := current
	for i := 0; i < len(players); i++ {
		index = (index + 1) % len(players)
		player := players[index]
		if !player.Folded && len(player.Hand) > 0 && player.Chips >= 0 {
			return index
		}
	}
	return -1
}

func (t *Table) drawLocked() Card {
	card := t.deck[0]
	t.deck = t.deck[1:]
	return card
}

func shuffledDeck() []Card {
	suits := []string{"S", "H", "D", "C"}
	deck := make([]Card, 0, 52)
	for _, suit := range suits {
		for rank := 2; rank <= 14; rank++ {
			deck = append(deck, Card{Rank: rank, Suit: suit})
		}
	}

	for i := len(deck) - 1; i > 0; i-- {
		j := secureInt(i + 1)
		deck[i], deck[j] = deck[j], deck[i]
	}
	return deck
}

func secureInt(max int) int {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return int(n.Int64())
}

func randomID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("player-%d", secureInt(1_000_000))
	}
	return hex.EncodeToString(buf)
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

type handScore struct {
	Category int
	Values   []int
}

func (h handScore) compare(other handScore) int {
	if h.Category != other.Category {
		if h.Category > other.Category {
			return 1
		}
		return -1
	}

	for i := 0; i < len(h.Values) && i < len(other.Values); i++ {
		if h.Values[i] > other.Values[i] {
			return 1
		}
		if h.Values[i] < other.Values[i] {
			return -1
		}
	}
	return 0
}

func (h handScore) label() string {
	labels := map[int]string{
		8: "Straight Flush",
		7: "Four of a Kind",
		6: "Full House",
		5: "Flush",
		4: "Straight",
		3: "Three of a Kind",
		2: "Two Pair",
		1: "One Pair",
		0: "High Card",
	}
	return labels[h.Category]
}

func bestHand(cards []Card) handScore {
	combinations := fiveCardCombos(cards)
	best := evaluateFive(combinations[0])
	for _, combo := range combinations[1:] {
		score := evaluateFive(combo)
		if score.compare(best) > 0 {
			best = score
		}
	}
	return best
}

func fiveCardCombos(cards []Card) [][]Card {
	var combos [][]Card
	n := len(cards)
	for a := 0; a < n-4; a++ {
		for b := a + 1; b < n-3; b++ {
			for c := b + 1; c < n-2; c++ {
				for d := c + 1; d < n-1; d++ {
					for e := d + 1; e < n; e++ {
						combos = append(combos, []Card{cards[a], cards[b], cards[c], cards[d], cards[e]})
					}
				}
			}
		}
	}
	return combos
}

func evaluateFive(cards []Card) handScore {
	rankCounts := map[int]int{}
	suitCounts := map[string]int{}
	ranks := make([]int, 0, 5)
	for _, card := range cards {
		rankCounts[card.Rank]++
		suitCounts[card.Suit]++
		ranks = append(ranks, card.Rank)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ranks)))

	flush := len(suitCounts) == 1
	straight, highStraight := detectStraight(ranks)

	type rankGroup struct {
		Rank  int
		Count int
	}
	var groups []rankGroup
	for rank, count := range rankCounts {
		groups = append(groups, rankGroup{Rank: rank, Count: count})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Rank > groups[j].Rank
	})

	if flush && straight {
		return handScore{Category: 8, Values: []int{highStraight}}
	}
	if groups[0].Count == 4 {
		return handScore{Category: 7, Values: []int{groups[0].Rank, groups[1].Rank}}
	}
	if groups[0].Count == 3 && groups[1].Count == 2 {
		return handScore{Category: 6, Values: []int{groups[0].Rank, groups[1].Rank}}
	}
	if flush {
		return handScore{Category: 5, Values: ranks}
	}
	if straight {
		return handScore{Category: 4, Values: []int{highStraight}}
	}
	if groups[0].Count == 3 {
		values := []int{groups[0].Rank}
		for _, group := range groups[1:] {
			values = append(values, group.Rank)
		}
		return handScore{Category: 3, Values: values}
	}
	if groups[0].Count == 2 && groups[1].Count == 2 {
		highPair := groups[0].Rank
		lowPair := groups[1].Rank
		kicker := groups[2].Rank
		return handScore{Category: 2, Values: []int{highPair, lowPair, kicker}}
	}
	if groups[0].Count == 2 {
		values := []int{groups[0].Rank}
		for _, group := range groups[1:] {
			values = append(values, group.Rank)
		}
		return handScore{Category: 1, Values: values}
	}
	return handScore{Category: 0, Values: ranks}
}

func detectStraight(ranks []int) (bool, int) {
	seen := map[int]bool{}
	var unique []int
	for _, rank := range ranks {
		if !seen[rank] {
			seen[rank] = true
			unique = append(unique, rank)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(unique)))

	if len(unique) != 5 {
		return false, 0
	}
	if unique[0]-unique[4] == 4 {
		return true, unique[0]
	}
	if unique[0] == 14 && unique[1] == 5 && unique[2] == 4 && unique[3] == 3 && unique[4] == 2 {
		return true, 5
	}
	return false, 0
}

package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	Width      = 20
	Height     = 20
	TickRate   = 60 * time.Millisecond
	RoundLimit = 5 * time.Minute
	MaxPlayers = 10
	NumObstacles = 8
)

const (
	MsgJoin     = "join"
	MsgState    = "state"
	MsgAction   = "action"
	MsgDead     = "dead"
	MsgInit     = "init"
	MsgRating   = "rating"
	MsgRound    = "round"
	MsgObstacle = "obstacle"
)

type Vector struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type Player struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Pos       Vector `json:"pos"`
	Dir       Vector `json:"dir"`
	Alive     bool   `json:"alive"`
	Wins      int    `json:"wins"`
	Streak    int    `json:"streak"`
	conn      *websocket.Conn
	writeChan chan interface{}
}

type Bullet struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	Dir   Vector `json:"dir"`
	Owner string `json:"owner"`
}

type Obstacle struct {
	X      int  `json:"x"`
	Y      int  `json:"y"`
	Width  int  `json:"width"`
	Height int  `json:"height"`
}

type GameState struct {
	Players   map[string]*Player `json:"players"`
	Bullets   []Bullet           `json:"bullets"`
	Obstacles []Obstacle         `json:"obstacles"`
	Round     int                `json:"round"`
}

type RatingEntry struct {
	Name   string `json:"name"`
	Wins   int    `json:"wins"`
	Streak int    `json:"streak"`
}

type Game struct {
	State   GameState
	mu      sync.RWMutex
	clients map[*websocket.Conn]*Player
}

var game = Game{
	State: GameState{
		Players:   make(map[string]*Player),
		Bullets:   make([]Bullet, 0),
		Obstacles: generateObstacles(),
		Round:     1,
	},
	clients: make(map[*websocket.Conn]*Player),
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func obstaclesIntersect(a, b Obstacle) bool {
	return !(a.X+a.Width+1 <= b.X ||
		b.X+b.Width+1 <= a.X ||
		a.Y+a.Height+1 <= b.Y ||
		b.Y+b.Height+1 <= a.Y)
}

func generateObstacles() []Obstacle {
	obstacles := make([]Obstacle, 0, NumObstacles)
	maxAttempts := 100

	for i := 0; i < NumObstacles; i++ {
		attempt := 0
		var newObstacle Obstacle
		validPosition := false

		for attempt < maxAttempts {
			width := rand.Intn(2) + 1  // 1-2
			height := rand.Intn(2) + 1 // 1-2
			
			x := rand.Intn(Width - width)
			y := rand.Intn(Height - height)
			
			newObstacle = Obstacle{
				X:      x,
				Y:      y,
				Width:  width,
				Height: height,
			}
			
			intersects := false
			for _, existing := range obstacles {
				if obstaclesIntersect(newObstacle, existing) {
					intersects = true
					break
				}
			}
			
			if !intersects {
				validPosition = true
				break
			}
			
			attempt++
		}
		
		if validPosition {
			obstacles = append(obstacles, newObstacle)
		}
	}
	return obstacles
}

func isCollidingWithObstacle(x, y int, obstacles []Obstacle) bool {
	for _, obs := range obstacles {
		if x >= obs.X && x < obs.X+obs.Width &&
			y >= obs.Y && y < obs.Y+obs.Height {
			return true
		}
	}
	return false
}

func isBulletBlocked(b Bullet, obstacles []Obstacle) bool {
	for _, obs := range obstacles {
		if b.X >= obs.X && b.X < obs.X+obs.Width &&
			b.Y >= obs.Y && b.Y < obs.Y+obs.Height {
			return true
		}
	}
	return false
}


func (g *Game) StartLoop() {
	ticker := time.NewTicker(TickRate)
	defer ticker.Stop()

	roundTimer := time.NewTicker(RoundLimit)
	defer roundTimer.Stop()

	for {
		select {
		case <-ticker.C:
			g.Update()
			g.BroadcastState()
			g.BroadcastRating()
		case <-roundTimer.C:
			g.mu.Lock()
			g.State.Round++
			g.resetRound()
			g.mu.Unlock()
		}
	}
}

func (g *Game) Update() {
	g.mu.Lock()
	defer g.mu.Unlock()

	activeBullets := []Bullet{}
	for _, b := range g.State.Bullets {
		b.X += b.Dir.X
		b.Y += b.Dir.Y

		if b.X < 0 || b.X >= Width || b.Y < 0 || b.Y >= Height {
			continue
		}

		if isBulletBlocked(b, g.State.Obstacles) {
			continue
		}

		hit := false
		for _, p := range g.State.Players {
			if p.Alive && p.Pos.X == b.X && p.Pos.Y == b.Y && p.ID != b.Owner {
				p.Alive = false
				hit = true
				
				p.Streak = 0
				break
			}
		}
		if !hit {
			activeBullets = append(activeBullets, b)
		}
	}
	g.State.Bullets = activeBullets

	aliveCount := 0
	lastAliveID := ""
	totalCount := len(g.State.Players)
	
	for _, p := range g.State.Players {
		if p.Alive {
			aliveCount++
			lastAliveID = p.ID
		}
	}

	if totalCount > 1 && aliveCount <= 1 {
		if aliveCount == 1 && lastAliveID != "" {
			if winner, exists := g.State.Players[lastAliveID]; exists {
				winner.Wins++
				winner.Streak++
			}
		}
		
		g.State.Round++
		g.resetRound()
		fmt.Printf("Round %d started!\n", g.State.Round)
	}
}

func (g *Game) resetRound() {
	g.State.Obstacles = generateObstacles()
	g.State.Bullets = []Bullet{}
	
	for _, p := range g.State.Players {
		p.Alive = true
		for {
			newPos := Vector{rand.Intn(Width), rand.Intn(Height)}
			if !isCollidingWithObstacle(newPos.X, newPos.Y, g.State.Obstacles) {
				p.Pos = newPos
				break
			}
		}
		p.Dir = Vector{0, 1}
	}
}

func (g *Game) BroadcastState() {
	g.mu.RLock()
	defer g.mu.RUnlock()

	stateMsg := map[string]interface{}{
		"type":  MsgState,
		"state": g.State,
	}

	for conn := range g.clients {
		select {
		case g.clients[conn].writeChan <- stateMsg:
		default:
		}
	}
}

func (g *Game) BroadcastRating() {
	g.mu.RLock()
	defer g.mu.RUnlock()

	rating := []RatingEntry{}
	for _, p := range g.State.Players {
		rating = append(rating, RatingEntry{
			Name:   p.Name,
			Wins:   p.Wins,
			Streak: p.Streak,
		})
	}

	ratingMsg := map[string]interface{}{
		"type":   MsgRating,
		"rating": rating,
	}

	for conn := range g.clients {
		select {
		case g.clients[conn].writeChan <- ratingMsg:
		default:
		}
	}
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer ws.Close()

	var initData map[string]interface{}
	err = ws.ReadJSON(&initData)
	if err != nil || initData["type"] != "join" {
		ws.WriteJSON(map[string]string{"error": "Need player name"})
		return
	}

	playerName, ok := initData["name"].(string)
	if !ok || playerName == "" {
		playerName = fmt.Sprintf("Player_%d", rand.Intn(10000))
	}

	playerID := fmt.Sprintf("p_%d", rand.Intn(10000))
	player := &Player{
		ID:        playerID,
		Name:      playerName,
		Pos:       Vector{rand.Intn(Width), rand.Intn(Height)},
		Dir:       Vector{0, 1},
		Alive:     true,
		Wins:      0,
		Streak:    0,
		conn:      ws,
		writeChan: make(chan interface{}, 256),
	}

	game.mu.Lock()
	if len(game.State.Players) >= MaxPlayers {
		game.mu.Unlock()
		ws.WriteJSON(map[string]string{"error": "Server is full"})
		return
	}
	
	for {
		if !isCollidingWithObstacle(player.Pos.X, player.Pos.Y, game.State.Obstacles) {
			break
		}
		player.Pos = Vector{rand.Intn(Width), rand.Intn(Height)}
	}
	
	game.State.Players[playerID] = player
	game.clients[ws] = player
	game.mu.Unlock()

	initMsg := map[string]interface{}{
		"type": MsgInit,
		"id":   playerID,
		"name": playerName,
	}
	if err := ws.WriteJSON(initMsg); err != nil {
		log.Println("Failed to send init:", err)
		return
	}

	go func() {
		for msg := range player.writeChan {
			if err := ws.WriteJSON(msg); err != nil {
				break
			}
		}
		close(player.writeChan)
	}()

	for {
		var msg map[string]interface{}
		err := ws.ReadJSON(&msg)
		if err != nil {
			game.mu.Lock()
			delete(game.State.Players, playerID)
			delete(game.clients, ws)
			game.mu.Unlock()
			break
		}

		if msg["type"] == MsgAction {
			action := msg["action"].(string)
			game.mu.Lock()
			if player.Alive {
				newX, newY := player.Pos.X, player.Pos.Y
				
				switch action {
				case "up":
					player.Dir = Vector{0, -1}
					newY = max(0, player.Pos.Y-1)
				case "down":
					player.Dir = Vector{0, 1}
					newY = min(Height-1, player.Pos.Y+1)
				case "left":
					player.Dir = Vector{-1, 0}
					newX = max(0, player.Pos.X-1)
				case "right":
					player.Dir = Vector{1, 0}
					newX = min(Width-1, player.Pos.X+1)
				case "shoot":
					game.State.Bullets = append(game.State.Bullets, Bullet{
						X: player.Pos.X, Y: player.Pos.Y,
						Dir: player.Dir, Owner: player.ID,
					})
				}
				
				if action != "shoot" && !isCollidingWithObstacle(newX, newY, game.State.Obstacles) {
					player.Pos.X = newX
					player.Pos.Y = newY
				}
			}
			game.mu.Unlock()
		}
	}
}

func min(a, b int) int { if a < b { return a }; return b }
func max(a, b int) int { if a > b { return a }; return b }

func main() {
	rand.Seed(time.Now().UnixNano())
	
	go game.StartLoop()

	http.HandleFunc("/ws", handleConnections)
	http.Handle("/", http.FileServer(http.Dir("./static")))

	fmt.Println("Server started on :8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

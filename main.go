package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	col  *mongo.Collection
	rdb  *redis.Client
	pgDB *sql.DB
	myDB *sql.DB
)

type Item struct {
	ID    string `json:"id" bson:"_id"`
	Name  string `json:"name" bson:"name"`
	Value string `json:"value" bson:"value"`
}

func main() {
	time.Sleep(2 * time.Second)

	// ── MongoDB ──
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://mongodb-svc:27017"
	}
	mClient, err := mongo.Connect(context.Background(), options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatalf("mongo connect: %v", err)
	}
	col = mClient.Database("multikind").Collection("items")
	log.Println("MongoDB connected")

	// ── Redis ──
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "redis-svc:6379"
	}
	rdb = redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Printf("redis ping warning: %v", err)
	} else {
		log.Println("Redis connected")
	}

	// ── PostgreSQL ──
	pgDSN := os.Getenv("PG_DSN")
	if pgDSN == "" {
		pgDSN = "postgres://postgres:postgres@postgres-svc:5432/testdb?sslmode=disable"
	}
	pgDB, err = sql.Open("postgres", pgDSN)
	if err != nil {
		log.Printf("postgres open warning: %v", err)
	} else if err := pgDB.Ping(); err != nil {
		log.Printf("postgres ping warning: %v", err)
	} else {
		pgDB.Exec("CREATE TABLE IF NOT EXISTS items (id SERIAL PRIMARY KEY, name TEXT)")
		log.Println("Postgres connected")
	}

	// ── MySQL ──
	myDSN := os.Getenv("MYSQL_DSN")
	if myDSN == "" {
		myDSN = "root:root@tcp(mysql-svc:3306)/testdb"
	}
	myDB, err = sql.Open("mysql", myDSN)
	if err != nil {
		log.Printf("mysql open warning: %v", err)
	} else if err := myDB.Ping(); err != nil {
		log.Printf("mysql ping warning: %v", err)
	} else {
		myDB.Exec("CREATE TABLE IF NOT EXISTS items (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(255))")
		log.Println("MySQL connected")
	}

	// ── Routes ──
	r := gin.Default()

	// Single-DB routes (to test each kind individually)
	r.GET("/redis/:val", handleRedisOnly)
	r.GET("/mongo/:val", handleMongoOnly)
	r.GET("/postgres/:val", handlePostgresOnly)
	r.GET("/mysql/:val", handleMySQLOnly)

	// Multi-DB routes (to test multi-kind)
	r.POST("/api/item", createItem)
	r.GET("/api/item/:id", getItem)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()
	log.Printf("multi-kind-app listening on :%s", port)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	log.Println("server exiting")
}

// ──────────── Single-DB Handlers ────────────

// handleRedisOnly — ONLY touches Redis. Should produce Kind: "Redis"
func handleRedisOnly(c *gin.Context) {
	val := c.Param("val")
	ctx := c.Request.Context()

	if err := rdb.Set(ctx, val, val, 10*time.Minute).Err(); err != nil {
		c.JSON(500, gin.H{"error": "redis SET: " + err.Error()})
		return
	}
	res, err := rdb.Get(ctx, val).Result()
	if err != nil {
		c.JSON(500, gin.H{"error": "redis GET: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"source": "redis", "value": res})
}

// handleMongoOnly — ONLY touches Mongo. Should produce Kind: "Mongo"
func handleMongoOnly(c *gin.Context) {
	val := c.Param("val")
	ctx := c.Request.Context()

	filter := bson.M{"_id": val}
	update := bson.M{"$set": bson.M{"_id": val, "value": val}}
	opts := options.Update().SetUpsert(true)
	if _, err := col.UpdateOne(ctx, filter, update, opts); err != nil {
		c.JSON(500, gin.H{"error": "mongo upsert: " + err.Error()})
		return
	}
	var doc bson.M
	if err := col.FindOne(ctx, filter).Decode(&doc); err != nil {
		c.JSON(500, gin.H{"error": "mongo find: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"source": "mongo", "doc": doc})
}

// handlePostgresOnly — ONLY touches Postgres. Should produce Kind: "Postgres"
func handlePostgresOnly(c *gin.Context) {
	val := c.Param("val")
	ctx := c.Request.Context()

	if _, err := pgDB.ExecContext(ctx, "INSERT INTO items(name) VALUES($1)", val); err != nil {
		c.JSON(500, gin.H{"error": "pg INSERT: " + err.Error()})
		return
	}
	var name string
	if err := pgDB.QueryRowContext(ctx, "SELECT name FROM items WHERE name=$1 LIMIT 1", val).Scan(&name); err != nil {
		c.JSON(500, gin.H{"error": "pg SELECT: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"source": "postgres", "value": name})
}

// handleMySQLOnly — ONLY touches MySQL. Should produce Kind: "MySQL"
func handleMySQLOnly(c *gin.Context) {
	val := c.Param("val")
	ctx := c.Request.Context()

	if _, err := myDB.ExecContext(ctx, "INSERT INTO items(name) VALUES(?)", val); err != nil {
		c.JSON(500, gin.H{"error": "mysql INSERT: " + err.Error()})
		return
	}
	var name string
	if err := myDB.QueryRowContext(ctx, "SELECT name FROM items WHERE name=? LIMIT 1", val).Scan(&name); err != nil {
		c.JSON(500, gin.H{"error": "mysql SELECT: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"source": "mysql", "value": name})
}

// ──────────── Multi-DB Handlers ────────────

func createItem(c *gin.Context) {
	var item Item
	if err := c.ShouldBindJSON(&item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()

	filter := bson.M{"_id": item.ID}
	update := bson.M{"$set": item}
	opts := options.Update().SetUpsert(true)
	if _, err := col.UpdateOne(ctx, filter, update, opts); err != nil {
		c.JSON(500, gin.H{"error": "mongo: " + err.Error()})
		return
	}
	if err := rdb.Set(ctx, "item:"+item.ID, item.Value, 10*time.Minute).Err(); err != nil {
		c.JSON(500, gin.H{"error": "redis: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"status": "created", "id": item.ID})
}

func getItem(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()

	var item Item
	if err := col.FindOne(ctx, bson.M{"_id": id}).Decode(&item); err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	cached, _ := rdb.Get(ctx, "item:"+id).Result()
	c.JSON(200, gin.H{"item": item, "redis_cached": cached})
}

package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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
	rdb       *redis.Client
	mongoColl *mongo.Collection
	pgDB      *sql.DB
	myDB      *sql.DB
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── Redis ──
	rdb = redis.NewClient(&redis.Options{
		Addr: env("REDIS_ADDR", "redis-test-svc:6379"),
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Printf("WARN: Redis not reachable: %v", err)
	} else {
		log.Println("Redis connected")
	}

	// ── MongoDB ──
	mongoURI := env("MONGO_URI", "mongodb://mongo-test-svc:27017")
	mc, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Printf("WARN: Mongo connect error: %v", err)
	} else {
		mongoColl = mc.Database("testdb").Collection("items")
		log.Println("Mongo connected")
	}

	// ── PostgreSQL ──
	pgDSN := env("PG_DSN", "postgres://testuser:testpass@postgres-test-svc:5432/testdb?sslmode=disable")
	pgDB, err = sql.Open("postgres", pgDSN)
	if err != nil {
		log.Printf("WARN: Postgres open error: %v", err)
	} else {
		pgDB.SetMaxOpenConns(5)
		if err := pgDB.PingContext(ctx); err != nil {
			log.Printf("WARN: Postgres ping error: %v", err)
		} else {
			log.Println("Postgres connected")
			_, _ = pgDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS items (id SERIAL PRIMARY KEY, name TEXT, created_at TIMESTAMP DEFAULT NOW())`)
		}
	}

	// ── MySQL ──
	myDSN := env("MYSQL_DSN", "testuser:testpass@tcp(mysql-test-svc:3306)/testdb?parseTime=true")
	myDB, err = sql.Open("mysql", myDSN)
	if err != nil {
		log.Printf("WARN: MySQL open error: %v", err)
	} else {
		myDB.SetMaxOpenConns(5)
		if err := myDB.PingContext(ctx); err != nil {
			log.Printf("WARN: MySQL ping error: %v", err)
		} else {
			log.Println("MySQL connected")
			_, _ = myDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS items (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(255), created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`)
		}
	}

	// ── Routes ──
	r := gin.Default()

	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	// Single-kind endpoints
	r.GET("/redis-only", handleRedisOnly)
	r.GET("/mongo-only", handleMongoOnly)
	r.GET("/postgres-only", handlePostgresOnly)
	r.GET("/mysql-only", handleMySQLOnly)
	r.GET("/http-only", handleHTTPOnly)

	// Multi-kind endpoints
	r.GET("/redis-mongo", handleRedisMongo)
	r.GET("/triple", handleTriple)
	r.GET("/all-dbs", handleAllDBs)
	r.GET("/kitchen-sink", handleKitchenSink)

	port := env("PORT", "8080")
	log.Printf("Starting multi-kind-app on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

// ── Single-kind handlers ──

func handleRedisOnly(c *gin.Context) {
	ctx := c.Request.Context()
	key := fmt.Sprintf("test-key-%d", time.Now().UnixNano())
	if err := rdb.Set(ctx, key, "hello-redis", 60*time.Second).Err(); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	val, err := rdb.Get(ctx, key).Result()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"source": "redis", "key": key, "value": val})
}

func handleMongoOnly(c *gin.Context) {
	ctx := c.Request.Context()
	doc := bson.M{"name": "test-item", "ts": time.Now().Unix()}
	_, err := mongoColl.InsertOne(ctx, doc)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	var result bson.M
	err = mongoColl.FindOne(ctx, bson.M{"name": "test-item"}).Decode(&result)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"source": "mongo", "document": result})
}

func handlePostgresOnly(c *gin.Context) {
	ctx := c.Request.Context()
	_, err := pgDB.ExecContext(ctx, `INSERT INTO items (name) VALUES ($1)`, "pg-item")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	var id int
	var name string
	err = pgDB.QueryRowContext(ctx, `SELECT id, name FROM items ORDER BY id DESC LIMIT 1`).Scan(&id, &name)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"source": "postgres", "id": id, "name": name})
}

func handleMySQLOnly(c *gin.Context) {
	ctx := c.Request.Context()
	_, err := myDB.ExecContext(ctx, `INSERT INTO items (name) VALUES (?)`, "mysql-item")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	var id int
	var name string
	err = myDB.QueryRowContext(ctx, `SELECT id, name FROM items ORDER BY id DESC LIMIT 1`).Scan(&id, &name)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"source": "mysql", "id": id, "name": name})
}

func handleHTTPOnly(c *gin.Context) {
	resp, err := http.Get("https://httpbin.org/get")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	c.JSON(200, gin.H{"source": "http", "status": resp.StatusCode, "bodyLen": len(body)})
}

// ── Multi-kind handlers ──

func handleRedisMongo(c *gin.Context) {
	ctx := c.Request.Context()

	// Redis
	rdb.Set(ctx, "multi-key", "from-redis", 60*time.Second)
	redisVal, _ := rdb.Get(ctx, "multi-key").Result()

	// Mongo
	mongoColl.InsertOne(ctx, bson.M{"name": "multi-item", "ts": time.Now().Unix()})
	var mongoDoc bson.M
	mongoColl.FindOne(ctx, bson.M{"name": "multi-item"}).Decode(&mongoDoc)

	c.JSON(200, gin.H{
		"redis": redisVal,
		"mongo": mongoDoc,
	})
}

func handleTriple(c *gin.Context) {
	ctx := c.Request.Context()

	// Redis
	rdb.Set(ctx, "triple-key", "value", 60*time.Second)
	redisVal, _ := rdb.Get(ctx, "triple-key").Result()

	// Mongo
	mongoColl.InsertOne(ctx, bson.M{"name": "triple-item", "ts": time.Now().Unix()})
	var mongoDoc bson.M
	mongoColl.FindOne(ctx, bson.M{"name": "triple-item"}).Decode(&mongoDoc)

	// Postgres
	pgDB.ExecContext(ctx, `INSERT INTO items (name) VALUES ($1)`, "triple-pg")
	var pgName string
	pgDB.QueryRowContext(ctx, `SELECT name FROM items ORDER BY id DESC LIMIT 1`).Scan(&pgName)

	c.JSON(200, gin.H{
		"redis":    redisVal,
		"mongo":    mongoDoc,
		"postgres": pgName,
	})
}

func handleAllDBs(c *gin.Context) {
	ctx := c.Request.Context()

	// Redis
	rdb.Set(ctx, "all-key", "value", 60*time.Second)
	redisVal, _ := rdb.Get(ctx, "all-key").Result()

	// Mongo
	mongoColl.InsertOne(ctx, bson.M{"name": "all-item", "ts": time.Now().Unix()})
	var mongoDoc bson.M
	mongoColl.FindOne(ctx, bson.M{"name": "all-item"}).Decode(&mongoDoc)

	// Postgres
	pgDB.ExecContext(ctx, `INSERT INTO items (name) VALUES ($1)`, "all-pg")
	var pgName string
	pgDB.QueryRowContext(ctx, `SELECT name FROM items ORDER BY id DESC LIMIT 1`).Scan(&pgName)

	// MySQL
	myDB.ExecContext(ctx, `INSERT INTO items (name) VALUES (?)`, "all-mysql")
	var myName string
	myDB.QueryRowContext(ctx, `SELECT name FROM items ORDER BY id DESC LIMIT 1`).Scan(&myName)

	c.JSON(200, gin.H{
		"redis":    redisVal,
		"mongo":    mongoDoc,
		"postgres": pgName,
		"mysql":    myName,
	})
}

func handleKitchenSink(c *gin.Context) {
	ctx := c.Request.Context()

	// Redis
	rdb.Set(ctx, "sink-key", "value", 60*time.Second)
	redisVal, _ := rdb.Get(ctx, "sink-key").Result()

	// Mongo
	mongoColl.InsertOne(ctx, bson.M{"name": "sink-item", "ts": time.Now().Unix()})
	var mongoDoc bson.M
	mongoColl.FindOne(ctx, bson.M{"name": "sink-item"}).Decode(&mongoDoc)

	// Postgres
	pgDB.ExecContext(ctx, `INSERT INTO items (name) VALUES ($1)`, "sink-pg")
	var pgName string
	pgDB.QueryRowContext(ctx, `SELECT name FROM items ORDER BY id DESC LIMIT 1`).Scan(&pgName)

	// MySQL
	myDB.ExecContext(ctx, `INSERT INTO items (name) VALUES (?)`, "sink-mysql")
	var myName string
	myDB.QueryRowContext(ctx, `SELECT name FROM items ORDER BY id DESC LIMIT 1`).Scan(&myName)

	// HTTP external call
	resp, err := http.Get("https://httpbin.org/get")
	httpStatus := 0
	if err == nil {
		httpStatus = resp.StatusCode
		resp.Body.Close()
	}

	c.JSON(200, gin.H{
		"redis":      redisVal,
		"mongo":      mongoDoc,
		"postgres":   pgName,
		"mysql":      myName,
		"httpStatus": httpStatus,
	})
}

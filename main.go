package main

import (
	"context"
	"database/sql"
	"log"
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

func main() {
	// Initialize ALL DB connections
	initRedis()
	initMongo()
	initPostgres()
	initMySQL()

	r := gin.Default()

	// Simplified Endpoints - Deterministic & Input-Driven
	r.GET("/redis/:val", handleRedis)
	r.GET("/mongo/:val", handleMongo)
	r.GET("/postgres/:val", handlePostgres)
	r.GET("/mysql/:val", handleMySQL)
	r.GET("/all/:val", handleAll)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Starting simplified multi-kind app on :%s", port)
	r.Run(":" + port)
}

// --- Handlers ---

func handleRedis(c *gin.Context) {
	val := c.Param("val")
	ctx := c.Request.Context()

	// SET key=val, value=val
	if err := rdb.Set(ctx, val, val, 60*time.Second).Err(); err != nil {
		c.JSON(500, gin.H{"error": "Redis SET failed: " + err.Error()})
		return
	}

	// GET key=val
	res, err := rdb.Get(ctx, val).Result()
	if err != nil {
		c.JSON(500, gin.H{"error": "Redis GET failed: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{"source": "redis", "key": val, "value": res})
}

func handleMongo(c *gin.Context) {
	val := c.Param("val")
	ctx := c.Request.Context()

	// Upsert document with _id=val
	filter := bson.M{"_id": val}
	update := bson.M{"$set": bson.M{"value": val}}
	opts := options.Update().SetUpsert(true)

	_, err := mongoColl.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		c.JSON(500, gin.H{"error": "Mongo UpdateOne failed: " + err.Error()})
		return
	}

	// Find document
	var res bson.M
	if err := mongoColl.FindOne(ctx, filter).Decode(&res); err != nil {
		c.JSON(500, gin.H{"error": "Mongo FindOne failed: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{"source": "mongo", "doc": res})
}

func handlePostgres(c *gin.Context) {
	val := c.Param("val")
	// Insert (ignore error if exists for simplicity, or we can create table without unique constraint)
	// We'll just insert.
	_, err := pgDB.ExecContext(c.Request.Context(), "INSERT INTO items(name) VALUES($1)", val)
	if err != nil {
		c.JSON(500, gin.H{"error": "Postgres INSERT failed: " + err.Error()})
		return
	}

	// Select
	var name string
	err = pgDB.QueryRowContext(c.Request.Context(), "SELECT name FROM items WHERE name=$1 ORDER BY id DESC LIMIT 1", val).Scan(&name)
	if err != nil {
		c.JSON(500, gin.H{"error": "Postgres SELECT failed: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{"source": "postgres", "value": name})
}

func handleMySQL(c *gin.Context) {
	val := c.Param("val")
	// Insert
	_, err := myDB.ExecContext(c.Request.Context(), "INSERT INTO items(name) VALUES(?)", val)
	if err != nil {
		c.JSON(500, gin.H{"error": "MySQL INSERT failed: " + err.Error()})
		return
	}

	// Select
	var name string
	err = myDB.QueryRowContext(c.Request.Context(), "SELECT name FROM items WHERE name=? ORDER BY id DESC LIMIT 1", val).Scan(&name)
	if err != nil {
		c.JSON(500, gin.H{"error": "MySQL SELECT failed: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{"source": "mysql", "value": name})
}

func handleAll(c *gin.Context) {
	val := c.Param("val")
	ctx := c.Request.Context()
	res := gin.H{"source": "all"}

	// 1. Redis
	if err := rdb.Set(ctx, val, val, 60*time.Second).Err(); err == nil {
		v, _ := rdb.Get(ctx, val).Result()
		res["redis"] = v
	} else {
		res["redis_error"] = err.Error()
	}

	// 2. Mongo
	filter := bson.M{"_id": val}
	update := bson.M{"$set": bson.M{"value": val}}
	opts := options.Update().SetUpsert(true)
	mongoColl.UpdateOne(ctx, filter, update, opts)
	var mDoc bson.M
	mongoColl.FindOne(ctx, filter).Decode(&mDoc)
	res["mongo"] = mDoc

	// 3. Postgres
	pgDB.ExecContext(ctx, "INSERT INTO items(name) VALUES($1)", val)
	var pgName string
	pgDB.QueryRowContext(ctx, "SELECT name FROM items WHERE name=$1 ORDER BY id DESC LIMIT 1", val).Scan(&pgName)
	res["postgres"] = pgName

	// 4. MySQL
	myDB.ExecContext(ctx, "INSERT INTO items(name) VALUES(?)", val)
	var myName string
	myDB.QueryRowContext(ctx, "SELECT name FROM items WHERE name=? ORDER BY id DESC LIMIT 1", val).Scan(&myName)
	res["mysql"] = myName

	c.JSON(200, res)
}

// --- Init Functions ---

func initRedis() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	rdb = redis.NewClient(&redis.Options{Addr: addr})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Printf("Warning: Failed to connect to Redis: %v", err)
	} else {
		log.Println("Redis connected")
	}
}

func initMongo() {
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}
	clientOptions := options.Client().ApplyURI(uri)
	client, err := mongo.Connect(context.Background(), clientOptions)
	if err != nil {
		log.Printf("Warning: Failed to connect to Mongo: %v", err)
		return
	}
	// Verify connection
	if err := client.Ping(context.Background(), nil); err != nil {
		log.Printf("Warning: Mongo Ping failed: %v", err)
	}
	mongoColl = client.Database("testdb").Collection("items")
	log.Println("Mongo connected")
}

func initPostgres() {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		dsn = "postgres://user:pass@localhost:5432/db?sslmode=disable"
	}
	var err error
	pgDB, err = sql.Open("postgres", dsn)
	if err != nil {
		log.Printf("Warning: Failed to open Postgres: %v", err)
		return
	}
	if err := pgDB.Ping(); err != nil {
		log.Printf("Warning: Postgres Ping failed: %v", err)
	} else {
		log.Println("Postgres connected")
		// Create table
		pgDB.Exec("CREATE TABLE IF NOT EXISTS items (id SERIAL PRIMARY KEY, name TEXT)")
	}
}

func initMySQL() {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		dsn = "user:pass@tcp(localhost:3306)/db"
	}
	var err error
	myDB, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Printf("Warning: Failed to open MySQL: %v", err)
		return
	}
	if err := myDB.Ping(); err != nil {
		log.Printf("Warning: MySQL Ping failed: %v", err)
	} else {
		log.Println("MySQL connected")
		// Create table
		myDB.Exec("CREATE TABLE IF NOT EXISTS items (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(255))")
	}
}

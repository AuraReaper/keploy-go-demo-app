package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	col *mongo.Collection
	rdb *redis.Client
)

// Item is stored in both Mongo and Redis.
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
		mongoURI = "mongodb://mongoDb:27017"
	}
	mClient, err := mongo.Connect(context.Background(),
		options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatalf("mongo connect: %v", err)
	}
	col = mClient.Database("multikind").Collection("items")
	log.Println("MongoDB connected")

	// ── Redis ──
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "redisDb:6379"
	}
	rdb = redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Printf("redis ping warning: %v", err)
	} else {
		log.Println("Redis connected")
	}

	// ── Routes ──
	r := gin.Default()
	r.POST("/api/item", createItem) // Mongo + Redis  → multi-kind mock
	r.GET("/api/item/:id", getItem) // Mongo + Redis  → multi-kind mock

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

	// graceful shutdown (same as gin-mongo sample)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	log.Println("server exiting")
}

// createItem stores an item in MongoDB AND caches its value in Redis.
// This single request generates mocks of Kind: DNS, Mongo, Redis.
func createItem(c *gin.Context) {
	var item Item
	if err := c.ShouldBindJSON(&item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()

	// 1. Upsert into Mongo
	filter := bson.M{"_id": item.ID}
	update := bson.M{"$set": item}
	opts := options.Update().SetUpsert(true)
	if _, err := col.UpdateOne(ctx, filter, update, opts); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "mongo: " + err.Error()})
		return
	}

	// 2. Cache in Redis
	if err := rdb.Set(ctx, "item:"+item.ID, item.Value, 10*time.Minute).Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "redis: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "created", "id": item.ID})
}

// getItem reads from both Mongo and Redis in every request.
// This ensures we always get multi-kind mocks (DNS + Mongo + Redis).
func getItem(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()

	// 1. Read from Mongo
	var item Item
	if err := col.FindOne(ctx, bson.M{"_id": id}).Decode(&item); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found in mongo"})
		return
	}

	// 2. Read from Redis (to generate Redis mock kind)
	cached, _ := rdb.Get(ctx, "item:"+id).Result()

	c.JSON(http.StatusOK, gin.H{
		"item":         item,
		"redis_cached": cached,
	})
}

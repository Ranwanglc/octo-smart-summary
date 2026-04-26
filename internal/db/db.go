package db

import (
	"log"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// New opens a GORM MySQL connection. dsn can be empty for test/mock scenarios.
func New(dsn string) (*gorm.DB, error) {
	if dsn == "" {
		return nil, nil
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetMaxIdleConns(10)

	log.Printf("[db] connected to MySQL: %s", maskDSN(dsn))
	return db, nil
}

func maskDSN(dsn string) string {
	if len(dsn) > 30 {
		return dsn[:15] + "***"
	}
	return "***"
}

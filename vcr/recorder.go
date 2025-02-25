package vcr

import (
	"errors"
	"os"
	"path"
	"sync"

	"database/sql"

	_ "github.com/mattn/go-sqlite3"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/utils"
)

type Recorder struct {
	logger logging.Logger
	dbPath string
	mu     sync.Mutex
	db     *sql.DB
}

func NewRecorder(dbPath string, logger logging.Logger) (*Recorder, error) {
	return &Recorder{dbPath: dbPath, logger: logger}, nil
}

func (rs *Recorder) Init(extradata []byte) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.db != nil {
		return errors.New("InitH265 called multiple times")
	}

	if err := os.MkdirAll(path.Dir(rs.dbPath), 0o755); err != nil {
		return err
	}

	db, err := sql.Open("sqlite3", rs.dbPath)
	if err != nil {
		return err
	}
	g := utils.NewGuard(func() {
		if cErr := db.Close(); cErr != nil {
			rs.logger.Error(cErr.Error())
		}
		if cErr := os.Remove(rs.dbPath); cErr != nil {
			rs.logger.Error(cErr.Error())
		}
	})

	sqlStmt := `
	CREATE TABLE extradata(id INTEGER NOT NULL PRIMARY KEY, data BLOB);
	CREATE TABLE packet(id INTEGER NOT NULL PRIMARY KEY, pts INTEGER, isIDR BOOLEAN, data BLOB);
	`

	if _, err := db.Exec(sqlStmt); err != nil {
		return err
	}

	if _, err = db.Exec("INSERT INTO extradata(data) VALUES(?);", extradata); err != nil {
		return err
	}
	g.Success()
	rs.db = db
	return nil
}

func (rs *Recorder) Packet(payload []byte, pts int64, isIDR bool) error {
	if _, err := rs.db.Exec("INSERT INTO packet(pts, isIDR, data) VALUES(?, ?, ?);", pts, isIDR, payload); err != nil {
		return err
	}
	return nil
}

func (rs *Recorder) Close() error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.db == nil {
		return nil
	}
	if err := rs.db.Close(); err != nil {
		return err
	}
	rs.db = nil
	return nil
}

package services

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	_ "github.com/lib/pq"
)

type AuthTokenProvider interface {
	Password() (string, error)
}

type LocalPassProvider struct {
	Pass string
}

func (lp *LocalPassProvider) Password() (string, error) {
	return lp.Pass, nil
}

type IAMAuthTokenProvider struct {
	Host           string
	Region         string
	MasterUsername string
}

func (ip *IAMAuthTokenProvider) Password() (string, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return "", err
	}
	authenticationToken, err := auth.BuildAuthToken(
		context.TODO(), ip.Host, ip.Region, ip.MasterUsername, cfg.Credentials)
	if err != nil {
		return "", err
	}
	return authenticationToken, nil
}

type RoleType int

const (
	ReadWrite RoleType = 0
	ReadOnly  RoleType = 1
)

const (
	ReadWriteSuffix string = "app_rw" // Read-Write role for app will have this suffix `<databasename>_app_rw`
	ReadOnlySuffix  string = "app_ro" // Read-Only role for app will have this suffix `<databasename>_app_ro`
)

type PostgresService struct {
	Host           string
	MasterUsername string // Admin User
	Port           string
	Region         string
	SSLMode        string
	AuthProvider   AuthTokenProvider
}

// GetDBClient create DB client for the given database
// using provided user (admin user)
func (p *PostgresService) getDBClient(dbname string) (*sql.DB, error) {
	pass, err := p.AuthProvider.Password()
	if err != nil {
		return nil, err
	}
	connStr := fmt.Sprintf("host=%s user=%s password=%s port=%s database=%s sslmode=%s",
		p.Host, p.MasterUsername, pass, p.Port, dbname, p.SSLMode)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}

	return db, nil
}

func (p *PostgresService) GetMgmtDBClient() (*sql.DB, error) {
	dbname := "postgres"
	return p.getDBClient(dbname)
}

func (p *PostgresService) CreateDB(dbname string) error {
	statement := fmt.Sprintf("CREATE DATABASE %s", dbname)
	dbClient, err := p.GetMgmtDBClient()
	if err != nil {
		return err
	}
	if _, err := dbClient.Exec(statement); err != nil {
		return err
	}
	return nil
}

// see https://www.postgresql.org/docs/12/ddl-priv.html &
// https://www.postgresql.org/docs/12/sql-grant.html
func (p *PostgresService) createRole(dbname string, roleType RoleType) (rolename string, err error) {
	var roleName string
	var grant string
	if roleType == ReadOnly {
		roleName = fmt.Sprintf("%s_%s", dbname, ReadOnlySuffix)
		grant = "CONNECT"
	}
	if roleType == ReadWrite {
		roleName = fmt.Sprintf("%s_%s", dbname, ReadWriteSuffix)
		grant = "ALL"
	}
	createStmt := fmt.Sprintf("CREATE ROLE %s", roleName)
	grantStmt := fmt.Sprintf("GRANT %s ON DATABASE %s TO %s", grant, dbname, roleName)

	dbClient, err := p.GetMgmtDBClient()
	if err != nil {
		return "", err
	}
	if _, err := dbClient.Exec(createStmt); err != nil && !AlreadyExist(err) {
		return "", err
	}
	if _, err := dbClient.Exec(grantStmt); err != nil {
		return "", err
	}
	// grant usage on public schema to RO role
	if roleType == ReadOnly {
		grantForSchema := fmt.Sprintf("GRANT USAGE ON SCHMEA public TO %s", roleName)
		if _, err := dbClient.Exec(grantForSchema); err != nil {
			return "", err
		}
	}

	return roleName, nil
}

func (p *PostgresService) CreateRWRole(dbname string) (roleName string, err error) {
	return p.createRole(dbname, ReadWrite)
}

func (p *PostgresService) CreateRORole(dbname string) (roleName string, err error) {
	return p.createRole(dbname, ReadOnly)
}

func AlreadyExist(err error) bool {
	return strings.Contains(err.Error(), "already exists")
}

// see https://www.postgresql.org/docs/12/sql-alterdefaultprivileges.html
func (p *PostgresService) ConfigureDefaultPrivileges(dbname, roleRW, roleRO string) (err error) {
	dbClient, err := p.getDBClient(dbname)
	if err != nil {
		return err
	}

	stmtRWTpls := []string{
		"ALTER DEFAULT PRIVILEGES FOR ROLE %s GRANT ALL ON TABLES TO %s",
		"ALTER DEFAULT PRIVILEGES FOR ROLE %s GRANT ALL ON SEQUENCES TO %s",
		"ALTER DEFAULT PRIVILEGES FOR ROLE %s GRANT ALL ON FUNCTIONS TO %s",
		"ALTER DEFAULT PRIVILEGES FOR ROLE %s GRANT ALL ON TYPES TO %s",
		"ALTER DEFAULT PRIVILEGES FOR ROLE %s GRANT ALL ON SCHEMAS TO %s",
	}
	// grantor: roleRW, grantee: Admin user
	for i := 0; i < len(stmtRWTpls); i++ {
		statement := fmt.Sprintf(stmtRWTpls[i], roleRW, p.MasterUsername)
		_, err := dbClient.Exec(statement)
		if err != nil {
			return fmt.Errorf("failed to excute statment: %s, error: %s", statement, err)
		}
	}

	// grantor: Admin User, grantee: roleRW
	for i := 0; i < len(stmtRWTpls); i++ {
		statement := fmt.Sprintf(stmtRWTpls[i], p.MasterUsername, roleRW)
		_, err := dbClient.Exec(statement)
		if err != nil {
			return fmt.Errorf("failed to excute statment: %s, error: %s", statement, err)
		}
	}

	stmtROTpls := []string{
		"ALTER DEFAULT PRIVILEGES FOR ROLE %s GRANT SELECT ON TABLES TO %s",
		"ALTER DEFAULT PRIVILEGES FOR ROLE %s GRANT USAGE ON SCHEMAS TO %s",
	}
	// grantor: roleRW, grantee: roleRO
	for i := 0; i < len(stmtROTpls); i++ {
		statement := fmt.Sprintf(stmtROTpls[i], roleRW, roleRO)
		_, err := dbClient.Exec(statement)
		if err != nil {
			return fmt.Errorf("failed to excute statment: %s, error: %s", statement, err)
		}
	}
	// grantor: Admin User, grantee: roleRO
	for i := 0; i < len(stmtROTpls); i++ {
		statement := fmt.Sprintf(stmtROTpls[i], p.MasterUsername, roleRO)
		_, err := dbClient.Exec(statement)
		if err != nil {
			return fmt.Errorf("failed to excute statment: %s, error: %s", statement, err)
		}
	}

	return nil
}

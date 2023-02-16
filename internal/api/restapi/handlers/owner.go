package handlers

import (
	"fmt"
	"github.com/go-openapi/runtime/middleware"
	"github.com/go-openapi/strfmt"
	"gitlab.com/comentario/comentario/internal/api/models"
	"gitlab.com/comentario/comentario/internal/api/restapi/operations"
	"gitlab.com/comentario/comentario/internal/config"
	"gitlab.com/comentario/comentario/internal/mail"
	"gitlab.com/comentario/comentario/internal/svc"
	"gitlab.com/comentario/comentario/internal/util"
	"golang.org/x/crypto/bcrypt"
	"time"
)

const ownersRowColumns = `
	owners.ownerHex,
	owners.email,
	owners.name,
	owners.confirmedEmail,
	owners.joinDate
`

func OwnerConfirmHex(params operations.OwnerConfirmHexParams) middleware.Responder {
	if params.ConfirmHex != "" {
		if err := ownerConfirmHex(params.ConfirmHex); err == nil {
			// Redirect to login
			return operations.NewOwnerConfirmHexTemporaryRedirect().
				WithLocation(config.URLFor("login", map[string]string{"confirmed": "true"}))
		}
	}

	// TODO: include error message in the URL
	return operations.NewOwnerConfirmHexTemporaryRedirect().
		WithLocation(config.URLFor("login", map[string]string{"confirmed": "false"}))
}

func OwnerDelete(params operations.OwnerDeleteParams) middleware.Responder {
	owner, err := ownerGetByOwnerToken(*params.Body.OwnerToken)
	if err != nil {
		return operations.NewOwnerDeleteOK().WithPayload(&models.APIResponseBase{Message: err.Error()})
	}

	if err = ownerDelete(owner.OwnerHex, false); err != nil {
		return operations.NewOwnerDeleteOK().WithPayload(&models.APIResponseBase{Message: err.Error()})
	}

	// Succeeded
	return operations.NewOwnerDeleteOK().WithPayload(&models.APIResponseBase{Success: true})
}

func OwnerLogin(params operations.OwnerLoginParams) middleware.Responder {
	ownerToken, err := ownerLogin(*params.Body.Email, *params.Body.Password)
	if err != nil {
		return operations.NewOwnerLoginOK().WithPayload(&operations.OwnerLoginOKBody{Message: err.Error()})
	}

	// Succeeded
	return operations.NewOwnerLoginOK().WithPayload(&operations.OwnerLoginOKBody{
		OwnerToken: ownerToken,
		Success:    true,
	})
}

func OwnerNew(params operations.OwnerNewParams) middleware.Responder {
	if _, err := ownerNew(*params.Body.Email, *params.Body.Name, *params.Body.Password); err != nil {
		return operations.NewOwnerNewOK().WithPayload(&operations.OwnerNewOKBody{Message: err.Error()})
	}

	// Errors in creating a commenter account should not hold this up
	_, _ = commenterNew(*params.Body.Email, *params.Body.Name, "undefined", "undefined", "commento", *params.Body.Password)

	return operations.NewOwnerNewOK().WithPayload(&operations.OwnerNewOKBody{
		ConfirmEmail: mail.SMTPConfigured,
		Success:      true,
	})
}

func OwnerSelf(params operations.OwnerSelfParams) middleware.Responder {
	// Try to find the owner
	owner, err := ownerGetByOwnerToken(*params.Body.OwnerToken)
	if err == util.ErrorNoSuchToken {
		return operations.NewOwnerSelfOK().WithPayload(&operations.OwnerSelfOKBody{Success: true})
	}

	if err != nil {
		return operations.NewOwnerSelfOK().WithPayload(&operations.OwnerSelfOKBody{Message: err.Error()})
	}

	// Succeeded
	return operations.NewOwnerSelfOK().WithPayload(&operations.OwnerSelfOKBody{
		LoggedIn: true,
		Owner:    owner,
		Success:  true,
	})
}

func ownerConfirmHex(confirmHex string) error {
	if confirmHex == "" {
		return util.ErrorMissingField
	}

	res, err := svc.DB.Exec(
		"update owners "+
			"set confirmedEmail=true where ownerHex in (select ownerHex from ownerConfirmHexes where confirmHex=$1);",
		confirmHex)
	if err != nil {
		logger.Errorf("cannot mark user's confirmedEmail as true: %v\n", err)
		return util.ErrorInternal
	}

	count, err := res.RowsAffected()
	if err != nil {
		logger.Errorf("cannot count rows affected: %v\n", err)
		return util.ErrorInternal
	}

	if count == 0 {
		return util.ErrorNoSuchConfirmationToken
	}

	_, err = svc.DB.Exec("delete from ownerConfirmHexes where confirmHex=$1;", confirmHex)
	if err != nil {
		logger.Warningf("cannot remove confirmation token: %v\n", err)
		// Don't return an error because this is not critical.
	}

	return nil
}

func ownerDelete(ownerHex models.HexID, deleteDomains bool) error {
	domains, err := domainList(ownerHex)
	if err != nil {
		return err
	}

	if len(domains) > 0 {
		if !deleteDomains {
			return util.ErrorCannotDeleteOwnerWithActiveDomains
		}
		for _, d := range domains {
			if err := domainDelete(d.Domain); err != nil {
				return err
			}
		}
	}

	_, err = svc.DB.Exec("delete from owners where ownerHex = $1;", ownerHex)
	if err != nil {
		return util.ErrorNoSuchOwner
	}

	_, err = svc.DB.Exec("delete from ownersessions where ownerHex = $1;", ownerHex)
	if err != nil {
		logger.Errorf("cannot delete from ownersessions: %v", err)
		return util.ErrorInternal
	}

	_, err = svc.DB.Exec("delete from resethexes where hex = $1;", ownerHex)
	if err != nil {
		logger.Errorf("cannot delete from resethexes: %v", err)
		return util.ErrorInternal
	}

	return nil
}

func ownerGetByEmail(email strfmt.Email) (*models.Owner, error) {
	if email == "" {
		return nil, util.ErrorMissingField
	}

	row := svc.DB.QueryRow(fmt.Sprintf("select %s from owners where email=$1;", ownersRowColumns), email)

	var o models.Owner
	if err := ownersRowScan(row, &o); err != nil {
		// TODO: Make sure this is actually no such email.
		return nil, util.ErrorNoSuchEmail
	}

	return &o, nil
}

func ownerGetByOwnerToken(ownerToken models.HexID) (*models.Owner, error) {
	if ownerToken == "" {
		return nil, util.ErrorMissingField
	}

	row := svc.DB.QueryRow(
		fmt.Sprintf(
			"select %s "+
				"from owners "+
				"where owners.ownerHex in "+
				"(select ownerSessions.ownerHex from ownerSessions where ownerSessions.ownerToken = $1);",
			ownersRowColumns),
		ownerToken)

	var o models.Owner
	if err := ownersRowScan(row, &o); err != nil {
		logger.Errorf("cannot scan owner: %v\n", err)
		return nil, util.ErrorInternal
	}

	return &o, nil
}

func ownerLogin(email strfmt.Email, password string) (models.HexID, error) {
	if email == "" || password == "" {
		return "", util.ErrorMissingField
	}

	row := svc.DB.QueryRow("select ownerHex, confirmedEmail, passwordHash from owners where email=$1;", email)

	var ownerHex string
	var confirmedEmail bool
	var passwordHash string
	if err := row.Scan(&ownerHex, &confirmedEmail, &passwordHash); err != nil {
		// Add a delay to discourage brute-force attacks
		time.Sleep(util.WrongAuthDelay)
		return "", util.ErrorInvalidEmailPassword
	}

	if !confirmedEmail {
		return "", util.ErrorUnconfirmedEmail
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		// TODO: is this the only possible error?
		// Add a delay to discourage brute-force attacks
		time.Sleep(util.WrongAuthDelay)
		return "", util.ErrorInvalidEmailPassword
	}

	ownerToken, err := util.RandomHex(32)
	if err != nil {
		logger.Errorf("cannot create ownerToken: %v", err)
		return "", util.ErrorInternal
	}

	_, err = svc.DB.Exec(
		"insert into ownerSessions(ownerToken, ownerHex, loginDate) values($1, $2, $3);",
		ownerToken,
		ownerHex,
		time.Now().UTC(),
	)
	if err != nil {
		logger.Errorf("cannot insert ownerSession: %v\n", err)
		return "", util.ErrorInternal
	}

	return models.HexID(ownerToken), nil
}

func ownerNew(email strfmt.Email, name string, password string) (string, error) {
	if email == "" || name == "" || password == "" {
		return "", util.ErrorMissingField
	}

	if !config.CLIFlags.AllowNewOwners {
		return "", util.ErrorNewOwnerForbidden
	}

	if _, err := ownerGetByEmail(email); err == nil {
		return "", util.ErrorEmailAlreadyExists
	}

	if err := EmailNew(email); err != nil {
		return "", util.ErrorInternal
	}

	ownerHex, err := util.RandomHex(32)
	if err != nil {
		logger.Errorf("cannot generate ownerHex: %v", err)
		return "", util.ErrorInternal
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		logger.Errorf("cannot generate hash from password: %v\n", err)
		return "", util.ErrorInternal
	}

	_, err = svc.DB.Exec(
		"insert into owners(ownerHex, email, name, passwordHash, joinDate, confirmedEmail) values($1, $2, $3, $4, $5, $6);",
		ownerHex,
		email,
		name,
		string(passwordHash),
		time.Now().UTC(),
		!mail.SMTPConfigured)
	if err != nil {
		// TODO: Make sure `err` is actually about conflicting UNIQUE, and not some
		// other error. If it is something else, we should probably return `errorInternal`.
		return "", util.ErrorEmailAlreadyExists
	}

	if mail.SMTPConfigured {
		confirmHex, err := util.RandomHex(32)
		if err != nil {
			logger.Errorf("cannot generate confirmHex: %v", err)
			return "", util.ErrorInternal
		}

		_, err = svc.DB.Exec(
			"insert into ownerConfirmHexes(confirmHex, ownerHex, sendDate) values($1, $2, $3);",
			confirmHex,
			ownerHex,
			time.Now().UTC())
		if err != nil {
			logger.Errorf("cannot insert confirmHex: %v\n", err)
			return "", util.ErrorInternal
		}

		if err = mail.SMTPOwnerConfirmHex(string(email), name, confirmHex); err != nil {
			return "", err
		}
	}

	return ownerHex, nil
}

func ownersRowScan(s util.Scanner, o *models.Owner) error {
	return s.Scan(
		&o.OwnerHex,
		&o.Email,
		&o.Name,
		&o.ConfirmedEmail,
		&o.JoinDate,
	)
}
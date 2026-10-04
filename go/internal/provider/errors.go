package provider

import "errors"

func asError(err error, target **AuthError) bool {
	return errors.As(err, target)
}

// AsInsufficientCredit 判断是否为积分/权益不足。
func AsInsufficientCredit(err error) bool {
	var target *InsufficientCreditError
	return errors.As(err, &target)
}

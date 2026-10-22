package controller

import "go.mongodb.org/mongo-driver/v2/bson"

func ParseObjectID(value string) (bson.ObjectID, error) {
	return bson.ObjectIDFromHex(value)
}

func ObjectIDOrZero(value string) bson.ObjectID {
	id, _ := ParseObjectID(value)
	return id
}

func ValidObjectID(value string) bool {
	_, err := ParseObjectID(value)
	return err == nil
}

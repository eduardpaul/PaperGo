variable "database_url" {
  type    = string
  default = "sqlite://data/papergo.db?_fk=1"
}

env "local" {
  url = var.database_url
  migration {
    dir = "file://migrations"
  }
}

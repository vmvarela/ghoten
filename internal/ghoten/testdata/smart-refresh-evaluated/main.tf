variable "region" {
  type = string
}

variable "ami" {
  type = string
}

provider "aws" {
  region = var.region
}

resource "aws_instance" "foo" {
  ami = var.ami
}

variable "aws_region" {
  type    = string
  default = "ap-southeast-1"
}

variable "environment" {
  type    = string
  default = "demo"
  validation {
    condition     = contains(["demo", "dev", "staging", "prod"], var.environment)
    error_message = "environment harus salah satu: demo, dev, staging, prod."
  }
}

variable "image_registry" {
  type    = string
  default = "ghcr.io/ajiperdana"
}

variable "image_tag" {
  type    = string
  default = "latest"
}

# ---- Sizing untuk demo budget ----
variable "rds_instance_class" {
  type    = string
  default = "db.t3.micro" # ~$15/month full, ~$5/month kalau di-stop daily
}

variable "redis_node_type" {
  type    = string
  default = "cache.t3.micro" # ~$16/month — only used kalau enable_redis=true
}

variable "fargate_cpu" {
  type    = number
  default = 256 # 0.25 vCPU
}

variable "fargate_memory" {
  type    = number
  default = 512 # 512 MiB
}

variable "service_replicas" {
  description = "Default replicas per service untuk demo"
  type        = number
  default     = 1
}

# ---- Cost optimization toggles ----
variable "enable_redis" {
  description = <<-EOT
    Enable ElastiCache Redis untuk distributed locking.

    Default: false → hemat $16/bulan. Reservation service auto-fallback ke
    in-memory locker (cukup untuk single-replica demo).

    Set true kalau:
      - Multi-replica reservation service di production
      - High contention rate untuk user-selected spot
      - Butuh distributed rate limit di gateway
  EOT
  type    = bool
  default = false
}

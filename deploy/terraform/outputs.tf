output "public_ip" {
  value       = aws_eip.demo.public_ip
  description = "Elastic IP — stable across stop/start, unlike the instance's default public IP."
}

output "ssh_command" {
  value = "ssh ubuntu@${aws_eip.demo.public_ip}"
}

output "console_url" {
  value = "http://${aws_eip.demo.public_ip}:3000"
}

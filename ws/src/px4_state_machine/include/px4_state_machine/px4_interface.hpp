#ifndef PX4_INTERFACE_HPP
#define PX4_INTERFACE_HPP

#include <px4_msgs/msg/vehicle_command.hpp>
#include <rclcpp/rclcpp.hpp>

class Px4Interface
{
public:
    Px4Interface(rclcpp::Node::SharedPtr node);
    
    void arm();
    void takeOff(float altitude);
    void switchToHoldMode();
    void land();

private:
    rclcpp::Publisher<px4_msgs::msg::VehicleCommand>::SharedPtr vehicle_command_publisher_;
    rclcpp::Node::SharedPtr node_;
};

#endif // PX4_INTERFACE_HPP
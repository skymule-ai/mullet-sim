cmake_minimum_required(VERSION 3.5)
project(px4_state_machine)

if(CMAKE_COMPILER_IS_GNUCXX OR CMAKE_CXX_COMPILER_ID MATCHES "Clang")
  add_compile_options(-Wall -Wextra -Wpedantic)
endif()

# Find dependencies
find_package(ament_cmake REQUIRED)
find_package(rclcpp REQUIRED)
find_package(rclpy REQUIRED)
find_package(px4_msgs REQUIRED)

# C++ things -------------------------------
# Include directories
include_directories(include)

# Source files
set(SOURCES
    src/drone_state_machine.cpp
    # src/px4_interface.cpp
    src/main.cpp
)

# Create executable
add_executable(${PROJECT_NAME} ${SOURCES})

# Link libraries
ament_target_dependencies(${PROJECT_NAME}
    rclcpp
    rclpy
    px4_msgs
)

# Install
install(TARGETS
    ${PROJECT_NAME}
    DESTINATION lib/${PROJECT_NAME}
)


# Python things -------------------------------
# ament_python_install_package(${PROJECT_NAME})

# install(PROGRAMS

#   DESTINATION lib/${PROJECT_NAME}
# )


ament_package()